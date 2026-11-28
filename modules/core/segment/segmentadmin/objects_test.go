package segmentadmin_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin/admintest"
)

const (
	prefixURL = "s3://bkt/members/"
	keyA      = "members/a.txt"
	keyB      = "members/b.txt"
	keyC      = "members/c.txt"
	bucket    = "bkt"
	dirMember = "members/"
)

func gz(t *testing.T, text string) []byte {
	t.Helper()

	var buf bytes.Buffer

	zw := gzip.NewWriter(&buf)
	_, err := zw.Write([]byte(text))
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	return buf.Bytes()
}

func TestParseObjectURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		url     string
		wantErr error
		want    segmentadmin.ObjectRef
	}{
		{
			name: "an object",
			url:  "s3://bkt/dir/ids.txt",
			want: segmentadmin.ObjectRef{Bucket: bucket, Key: "dir/ids.txt"},
		},
		{name: "a prefix", url: prefixURL, want: segmentadmin.ObjectRef{Bucket: bucket, Key: dirMember, Prefix: true}},
		{name: "the whole bucket", url: "s3://bkt", want: segmentadmin.ObjectRef{Bucket: bucket, Prefix: true}},
		{name: "another scheme", url: "https://bkt/x", wantErr: segmentadmin.ErrObjectURL},
		{name: "no bucket", url: "s3:///x", wantErr: segmentadmin.ErrObjectURL},
		{name: "a query", url: "s3://bkt/x?versionId=1", wantErr: segmentadmin.ErrObjectURL},
		{name: "not a url", url: "s3://bk t/%zz", wantErr: segmentadmin.ErrObjectURL},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := segmentadmin.ParseObjectURL(tt.url)

			require.ErrorIs(t, err, tt.wantErr)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestObjectSourceImport covers the object and prefix sources of an import.
func TestObjectSourceImport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr        error
		setup          func(t *testing.T, objs *admintest.Objects)
		name           string
		url            string
		wantLive       []string
		opts           segmentadmin.ImportOptions
		wantAdded      int64
		maxObjects     int
		wantFinger     bool
		wantSwapped    bool
		wantOldRemains bool
	}{
		{
			name: "one object", url: "s3://bkt/members/a.txt",
			setup:    func(_ *testing.T, objs *admintest.Objects) { objs.Put(keyA, []byte("x\ny\n"), false) },
			wantLive: []string{"x", "y"}, wantAdded: 2, wantSwapped: true, wantFinger: true,
		},
		{
			name: "a prefix over several pages, a last line without a break", url: prefixURL,
			setup: func(_ *testing.T, objs *admintest.Objects) {
				objs.Put(keyA, []byte("a1\na2"), false)
				objs.Put(keyB, []byte("b1"), false)
				objs.Put(keyC, []byte("c1\nc2\n"), false)
				objs.Put("members/d.txt", []byte("d1\n"), false)
				objs.Put("members/folder/", nil, false)
				objs.Put("other/e.txt", []byte("zz\n"), false)
			},
			wantLive:  []string{"a1", "a2", "b1", "c1", "c2", "d1"},
			wantAdded: 6, wantSwapped: true, wantFinger: true,
		},
		{
			name: "gzip by header and by suffix, and a plain file named gz", url: prefixURL,
			setup: func(t *testing.T, objs *admintest.Objects) {
				t.Helper()

				objs.Put(keyA, gz(t, "h1\nh2\n"), true)
				objs.Put("members/b.txt.gz", gz(t, "s1\n"), false)
				objs.Put(keyC+".gz", []byte("p1\n"), false)
			},
			wantLive:  []string{"h1", "h2", "p1", "s1"},
			wantAdded: 4, wantSwapped: true, wantFinger: true,
		},
		{
			name: "a broken gzip stream", url: "s3://bkt/members/a.txt",
			setup: func(t *testing.T, objs *admintest.Objects) {
				t.Helper()

				objs.Put(keyA, gz(t, "h1\nh2\n")[:12], true)
			},
			wantErr: io.ErrUnexpectedEOF, wantOldRemains: true,
		},
		{
			name: "a read error in the middle of a stream", url: prefixURL,
			setup: func(_ *testing.T, objs *admintest.Objects) {
				objs.Put(keyA, []byte("a1\n"), false)
				objs.Put(keyB, []byte("b1\nb2\nb3\n"), false)
				objs.BreakAfter(keyB, 4)
			},
			wantErr: admintest.ErrReadFailed, wantOldRemains: true,
		},
		{
			name: "an object that was replaced after the listing", url: prefixURL,
			setup: func(_ *testing.T, objs *admintest.Objects) {
				objs.Put(keyA, []byte("a1\n"), false)
				objs.Put(keyB, []byte("b1\n"), false)
				objs.OnGet(func(key string) {
					if key == keyB {
						objs.Put(keyB, []byte("b2\n"), false)
					}
				})
			},
			wantErr: segmentadmin.ErrObjectChanged, wantOldRemains: true,
		},
		{
			name: "an object that was added after the listing", url: prefixURL,
			setup: func(_ *testing.T, objs *admintest.Objects) {
				objs.Put(keyA, []byte("a1\n"), false)
				objs.OnGet(func(string) { objs.Put(keyC, []byte("c1\n"), false) })
			},
			wantErr: segmentadmin.ErrSourceChanged, wantOldRemains: true,
		},
		{
			name: "more objects than the bound", url: prefixURL, maxObjects: 2,
			setup: func(_ *testing.T, objs *admintest.Objects) {
				objs.Put(keyA, []byte("a1\n"), false)
				objs.Put(keyB, []byte("b1\n"), false)
				objs.Put(keyC, []byte("c1\n"), false)
			},
			wantErr: segmentadmin.ErrTooManyObjects, wantOldRemains: true,
		},
		{
			name: "an empty prefix", url: prefixURL, setup: func(*testing.T, *admintest.Objects) {},
			wantErr: segmentadmin.ErrEmptyInput, wantOldRemains: true,
		},
		{
			name: "an empty prefix with --allow-empty", url: prefixURL, setup: func(*testing.T, *admintest.Objects) {},
			opts: segmentadmin.ImportOptions{AllowEmpty: true}, wantSwapped: true, wantFinger: true,
			wantLive: []string{},
		},
		{
			name: "a missing object", url: "s3://bkt/members/none.txt", setup: func(*testing.T, *admintest.Objects) {},
			wantErr: segmentadmin.ErrObjectMissing, wantOldRemains: true,
		},
		{
			name: "a failed listing", url: prefixURL,
			setup:   func(_ *testing.T, objs *admintest.Objects) { objs.FailHeads(1) },
			wantErr: admintest.ErrReadFailed, wantOldRemains: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store, objs := admintest.New(), admintest.NewObjects(2)
			_, err := store.SAdd(t.Context(), liveKey, []string{memberJunk}, 0)
			require.NoError(t, err)

			tt.setup(t, objs)

			ref, err := segmentadmin.ParseObjectURL(tt.url)
			require.NoError(t, err)

			stream, err := segmentadmin.NewObjectSource(objs, ref, tt.maxObjects).Open(
				t.Context(), "", segmentadmin.ReaderOptions{})

			var rep *segmentadmin.Report

			if err == nil {
				defer stream.Close()

				tt.opts.Fingerprint = stream.Fingerprint()
				rep, err = segmentadmin.Import(t.Context(), store, vipTarget(), stream, tt.opts)
			}

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantSwapped, rep.Swapped)
				assert.Equal(t, tt.wantAdded, rep.Added)
			}

			if tt.wantOldRemains {
				assert.Equal(t, []string{memberJunk}, store.Members(liveKey))
			} else {
				assert.Equal(t, tt.wantLive, store.Members(liveKey))
			}

			recorded, found, valueErr := store.Value(t.Context(), sourceKey)
			require.NoError(t, valueErr)
			assert.Equal(t, tt.wantFinger, found)
			assert.Equal(t, tt.wantFinger, recorded != "")
		})
	}
}

func TestObjectSourceFingerprint(t *testing.T) {
	t.Parallel()

	objs := admintest.NewObjects(1)
	objs.Put(keyA, []byte("a\n"), false)
	objs.Put(keyB, []byte("b\n"), false)

	ref := segmentadmin.ObjectRef{Bucket: bucket, Key: dirMember, Prefix: true}
	src := segmentadmin.NewObjectSource(objs, ref, 0)

	first, err := src.Fingerprint(t.Context())
	require.NoError(t, err)

	same, err := src.Fingerprint(t.Context())
	require.NoError(t, err)
	assert.Equal(t, first, same)

	objs.Put(keyB, []byte("c\n"), false)

	changed, err := src.Fingerprint(t.Context())
	require.NoError(t, err)
	assert.NotEqual(t, first, changed)

	_, err = src.Open(t.Context(), first, segmentadmin.ReaderOptions{})
	require.ErrorIs(t, err, segmentadmin.ErrSourceChanged)

	_, err = src.Open(t.Context(), "", segmentadmin.ReaderOptions{Format: "bad"})
	require.ErrorIs(t, err, segmentadmin.ErrBadOptions)

	stream, err := src.Open(t.Context(), changed, segmentadmin.ReaderOptions{})
	require.NoError(t, err)
	assert.Equal(t, 2, stream.Objects())
	require.NoError(t, stream.Close())
}
