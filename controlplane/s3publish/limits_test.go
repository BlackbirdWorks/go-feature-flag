package s3publish_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/controlplane/s3publish"
)

func TestDefaultLimitsAreTheOnesOfTheReader(t *testing.T) {
	t.Parallel()

	assert.Equal(t, controlplane.DefaultMaxObjectBytes, int64(16<<20))
	assert.Equal(t, controlplane.DefaultMaxTotalBytes, int64(1<<30))
	assert.Equal(t, controlplane.DefaultMaxManifestBytes, int64(16<<20))
	assert.Equal(t, 8, s3publish.DefaultConcurrency)
}

func TestNewRejectsNegativeLimits(t *testing.T) {
	t.Parallel()

	for _, mutate := range []func(*s3publish.Config){
		func(c *s3publish.Config) { c.MaxObjectBytes = -1 },
		func(c *s3publish.Config) { c.MaxTotalBytes = -1 },
		func(c *s3publish.Config) { c.MaxManifestBytes = -1 },
		func(c *s3publish.Config) { c.MaxObjects = -1 },
		func(c *s3publish.Config) { c.Concurrency = -1 },
	} {
		cfg := testConfig()
		mutate(&cfg)

		pub, err := s3publish.New(newFake(), &cfg)
		require.ErrorIs(t, err, s3publish.ErrConfig)
		assert.Nil(t, pub)
	}
}

func TestPublishRefusesWhatTheReaderRejects(t *testing.T) {
	t.Parallel()

	flags := flagsBody(t, "a")
	other := flagsBody(t, "b")
	third := flagsBody(t, "c")
	huge := append(flagsBody(t, "h"), "#"+strings.Repeat("x", int(controlplane.DefaultMaxObjectBytes))+"\n"...)

	tests := []struct {
		mutate    func(*s3publish.Config)
		name      string
		wantInErr string
		objects   []s3publish.Object
	}{
		{
			name:   "an object over the limit names the object and its file",
			mutate: func(c *s3publish.Config) { c.MaxObjectBytes = int64(len(flags)) - 1 },
			objects: []s3publish.Object{
				{
					Name:   "a",
					Kind:   controlplane.KindFlags,
					Format: controlplane.FormatYAML,
					Source: "flags/a.yaml",
					Body:   flags,
				},
			},
			wantInErr: "a (flags/a.yaml): size",
		},
		{
			name: "an empty object",
			objects: []s3publish.Object{
				{
					Name:   "empty",
					Kind:   controlplane.KindFlags,
					Format: controlplane.FormatYAML,
					Source: "flags/empty.yaml",
				},
			},
			wantInErr: "empty (flags/empty.yaml): size 0, an object must not be empty",
		},
		{
			name: "the default limit of the reader is 16 MiB",
			objects: []s3publish.Object{
				{Name: "huge", Kind: controlplane.KindFlags, Format: controlplane.FormatYAML, Body: huge},
			},
			wantInErr: "is over the limit of 16777216 bytes",
		},
		{
			name:   "the total of distinct objects is over the limit",
			mutate: func(c *s3publish.Config) { c.MaxTotalBytes = int64(len(flags) + len(other) - 1) },
			objects: []s3publish.Object{
				{Name: "a", Kind: controlplane.KindFlags, Format: controlplane.FormatYAML, Body: flags},
				{Name: "b", Kind: controlplane.KindFlags, Format: controlplane.FormatYAML, Body: other},
			},
			wantInErr: "objects total",
		},
		{
			name:   "a manifest over the limit",
			mutate: func(c *s3publish.Config) { c.MaxManifestBytes = 100 },
			objects: []s3publish.Object{
				{Name: "a", Kind: controlplane.KindFlags, Format: controlplane.FormatYAML, Body: flags},
			},
			wantInErr: "bytes, over the limit of 100 bytes",
		},
		{
			name:   msgTooMany,
			mutate: func(c *s3publish.Config) { c.MaxObjects = 2 },
			objects: []s3publish.Object{
				{Name: "a", Kind: controlplane.KindFlags, Format: controlplane.FormatYAML, Body: flags},
				{Name: "b", Kind: controlplane.KindFlags, Format: controlplane.FormatYAML, Body: other},
				{Name: "c", Kind: controlplane.KindFlags, Format: controlplane.FormatYAML, Body: third},
			},
			wantInErr: "the limit is 2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := newFake()
			pub := newPublisher(t, fake, tt.mutate)

			_, err := pub.Publish(t.Context(), s3publish.Request{Objects: tt.objects})
			require.ErrorIs(t, err, s3publish.ErrInvalid)
			assert.Contains(t, err.Error(), tt.wantInErr)
			assert.Empty(t, fake.Calls(), "the limits are checked before any S3 call")
			assert.NotContains(t, err.Error(), "flag-a", "content is never part of the error")
		})
	}
}

func TestPublishCountsEqualContentOnceInTheTotal(t *testing.T) {
	t.Parallel()

	body := flagsBody(t, "a")
	fake := newFake()
	pub := newPublisher(t, fake, func(c *s3publish.Config) { c.MaxTotalBytes = int64(len(body)) })

	_, err := pub.Publish(t.Context(), s3publish.Request{Objects: []s3publish.Object{
		{Name: nameOne, Kind: controlplane.KindFlags, Format: controlplane.FormatYAML, Body: body},
		{Name: nameTwo, Kind: controlplane.KindFlags, Format: controlplane.FormatYAML, Body: body},
	}})
	require.NoError(t, err, "two entries with one checksum are one download for the reader")
}

// TestVerifyAppliesTheWholeManifestLimits: what makes the reader reject the whole manifest is one problem of the
// manifest and nothing is read.
func TestVerifyAppliesTheWholeManifestLimits(t *testing.T) {
	t.Parallel()

	body := flagsBody(t, "a")
	other := flagsBody(t, "b")

	tests := []struct {
		mutate func(*s3publish.Config)
		name   string
	}{
		{name: "total over the limit", mutate: func(c *s3publish.Config) { c.MaxTotalBytes = int64(len(body)) }},
		{name: msgTooMany, mutate: func(c *s3publish.Config) { c.MaxObjects = 1 }},
		{name: "manifest over the limit", mutate: func(c *s3publish.Config) { c.MaxManifestBytes = 50 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := newFake()
			_, err := newPublisher(t, fake, nil).Publish(t.Context(), s3publish.Request{Objects: []s3publish.Object{
				{Name: "a", Kind: controlplane.KindFlags, Format: controlplane.FormatYAML, Body: body},
				{Name: "b", Kind: controlplane.KindFlags, Format: controlplane.FormatYAML, Body: other},
			}})
			require.NoError(t, err)

			rep, err := newPublisher(t, fake, tt.mutate).Verify(t.Context())
			require.NoError(t, err)
			require.Len(t, rep.Problems, 1)
			assert.Equal(t, s3publish.ProblemManifestInvalid, rep.Problems[0].Code)
			assert.Zero(t, rep.Checked, "nothing is read from a manifest that the reader would reject")
		})
	}
}

// TestVerifyFailsABadEntryAloneLikeTheReader: an entry that the reader never requests is a problem of that entry;
// the other entries are still read and checked, and the manifest itself stays valid.
func TestVerifyFailsABadEntryAloneLikeTheReader(t *testing.T) {
	t.Parallel()

	good1, good2 := flagsBody(t, "g1"), flagsBody(t, "g2")
	bad := flagsBody(t, "bad")

	tests := []struct {
		mutate     func(entry *controlplane.ManifestObject)
		name       string
		wantDetail string
		maxObject  int64
	}{
		{
			name:       "size 0",
			mutate:     func(e *controlplane.ManifestObject) { e.Size = 0 },
			wantDetail: "size 0, an object must not be empty",
		},
		{
			name: "size over the object limit", maxObject: int64(len(bad)) - 1, wantDetail: "is over the limit of",
			mutate: func(*controlplane.ManifestObject) {},
		},
		{
			name:       "key with a leading slash",
			mutate:     func(e *controlplane.ManifestObject) { e.Key = "/" + e.Key },
			wantDetail: "not an absolute bucket key",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := newFake()
			entries := make([]controlplane.ManifestObject, 0, 3)

			for _, doc := range []struct {
				name string
				body []byte
			}{{badEntry, bad}, {"b-good", good1}, {"c-good", good2}} {
				key := objectKey(t, doc.body)
				fake.Seed(key, doc.body)

				entries = append(entries, controlplane.ManifestObject{
					Name:       doc.name,
					Kind:       controlplane.KindFlags,
					Connection: testConnection,
					Key:        key,
					Format:     controlplane.FormatYAML,
					Checksum:   controlplane.ChecksumOf(doc.body),
					Size:       int64(len(doc.body)),
				})
			}

			tt.mutate(&entries[0])

			body, err := controlplane.MarshalManifest(&controlplane.Manifest{
				Channel: testChannel, Generation: 2, Objects: entries,
			})
			require.NoError(t, err)
			fake.Seed(manifestKey, body)
			fake.ResetCalls()

			rep, err := newPublisher(
				t,
				fake,
				func(c *s3publish.Config) { c.MaxObjectBytes = tt.maxObject },
			).Verify(t.Context())
			require.NoError(t, err)

			require.Len(t, rep.Problems, 1, "%v", rep.Problems)
			assert.Equal(t, badEntry, rep.Problems[0].Object)
			assert.Equal(t, s3publish.ProblemInvalidEntry, rep.Problems[0].Code)
			assert.Contains(t, rep.Problems[0].Detail, tt.wantDetail)
			assert.Equal(t, 2, rep.Checked, "the two good entries are checked")
			assert.Equal(
				t,
				s3publish.Requests{Get: 3},
				rep.Requests,
				"the manifest and the two good entries, nothing for the bad one",
			)
			assert.Equal(t, []string{
				"GET " + manifestKey, "GET " + entries[1].Key, "GET " + entries[2].Key,
			}, fake.Calls())
		})
	}
}

func TestAnExistingManifestWithABadEntryIsPublishableOverWithoutTheFlag(t *testing.T) {
	t.Parallel()

	body := flagsBody(t, "old")
	key := objectKey(t, body)

	fake := newFake()
	fake.Seed(key, body)

	manifest, err := controlplane.MarshalManifest(&controlplane.Manifest{
		Channel: testChannel, Generation: 4,
		Objects: []controlplane.ManifestObject{{
			Name: "empty", Kind: controlplane.KindFlags, Connection: testConnection, Key: key,
			Format: controlplane.FormatYAML, Checksum: controlplane.ChecksumOf(body), Size: 0,
		}},
	})
	require.NoError(t, err)

	etag := fake.Seed(manifestKey, manifest)

	res, err := newPublisher(t, fake, nil).Publish(t.Context(), s3publish.Request{Objects: standardObjects(t, "new")})
	require.NoError(t, err, "the reader keeps such a manifest, so it is a valid manifest to publish over")

	assert.Equal(t, uint64(5), res.Generation)
	assert.Contains(t, fake.Calls(), "PUT "+manifestKey+" if-match="+etag)
}

// TestVerifyClassificationTable pins the table of docs/tooling/publish.md and docs/go-module/s3publish.md: which
// problems of an entry fail that entry alone (the other entries are checked) and which make the whole manifest
// invalid (nothing is read). A change of the code or of ValidateManifest that moves a row fails here.
func TestVerifyClassificationTable(t *testing.T) {
	t.Parallel()

	const (
		entryGets = 3 // the manifest and the two good objects
		wholeGets = 1 // the manifest only
	)

	wholeRaw := func(name string, raw []byte) classificationRow {
		return classificationRow{
			name:       name,
			raw:        raw,
			wantCode:   s3publish.ProblemManifestInvalid,
			wantObject: manifestObject,
			wantGets:   wholeGets,
		}
	}
	whole := func(name string, mutate func(e []controlplane.ManifestObject)) classificationRow {
		return classificationRow{
			name:       name,
			mutate:     mutate,
			wantCode:   s3publish.ProblemManifestInvalid,
			wantObject: manifestObject,
			wantGets:   wholeGets,
		}
	}
	entry := func(name string, mutate func(e []controlplane.ManifestObject)) classificationRow {
		return classificationRow{
			name: name, mutate: mutate, wantCode: s3publish.ProblemInvalidEntry, wantObject: badEntry, wantChecked: 2,
			wantGets: entryGets,
		}
	}

	tests := []classificationRow{
		entry("size 0", func(e []controlplane.ManifestObject) { e[0].Size = 0 }),
		entry("size over the object limit", func(e []controlplane.ManifestObject) {
			e[0].Size = controlplane.DefaultMaxObjectBytes + 1
		}),
		entry("key with a leading slash", func(e []controlplane.ManifestObject) { e[0].Key = "/" + e[0].Key }),
		whole("negative size", func(e []controlplane.ManifestObject) { e[0].Size = -1 }),
		whole("empty key", func(e []controlplane.ManifestObject) { e[0].Key = "" }),
		whole("bad checksum", func(e []controlplane.ManifestObject) { e[0].Checksum.Value = "not-hex" }),
		whole("bad name", func(e []controlplane.ManifestObject) { e[0].Name = badName }),
		whole("unknown kind", func(e []controlplane.ManifestObject) { e[0].Kind = otherName }),
		whole("unknown format", func(e []controlplane.ManifestObject) { e[0].Format = "xml" }),
		wholeRaw("not parseable", []byte("objects: [")),
		{
			name: "wrong document kind, with fetchable entries", edit: func(b string) string {
				return strings.Replace(b, "kind: Manifest", "kind: FlagSet", 1)
			},
			wantCode: s3publish.ProblemManifestInvalid, wantObject: manifestObject, wantGets: wholeGets,
		},
		{
			name: "unsupported apiVersion, with fetchable entries", edit: func(b string) string {
				return strings.Replace(b, "apiVersion: semaphore/v1", "apiVersion: semaphore/v2", 1)
			},
			wantCode: s3publish.ProblemManifestInvalid, wantObject: manifestObject, wantGets: wholeGets,
		},
		{
			name: "too many entries", config: func(c *s3publish.Config) { c.MaxObjects = 2 },
			wantCode: s3publish.ProblemManifestInvalid, wantObject: manifestObject, wantGets: wholeGets,
		},
		{
			name: "distinct objects over the total limit", config: func(c *s3publish.Config) { c.MaxTotalBytes = 1 },
			wantCode: s3publish.ProblemManifestInvalid, wantObject: manifestObject, wantGets: wholeGets,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := newFake()
			entries := make([]controlplane.ManifestObject, 0, 3)

			for _, doc := range []struct {
				name string
				body []byte
			}{{badEntry, flagsBody(t, "bad")}, {"b-good", flagsBody(t, "g1")}, {"c-good", flagsBody(t, "g2")}} {
				key := objectKey(t, doc.body)
				fake.Seed(key, doc.body)

				entries = append(entries, controlplane.ManifestObject{
					Name:       doc.name,
					Kind:       controlplane.KindFlags,
					Connection: testConnection,
					Key:        key,
					Format:     controlplane.FormatYAML,
					Checksum:   controlplane.ChecksumOf(doc.body),
					Size:       int64(len(doc.body)),
				})
			}

			if tt.mutate != nil {
				tt.mutate(entries)
			}

			body := tt.raw
			if body == nil {
				var err error

				body, err = controlplane.MarshalManifest(
					&controlplane.Manifest{Channel: testChannel, Generation: 2, Objects: entries},
				)
				require.NoError(t, err)
			}

			if tt.edit != nil {
				body = []byte(tt.edit(string(body)))
			}

			fake.Seed(manifestKey, body)
			fake.ResetCalls()

			rep, err := newPublisher(t, fake, tt.config).Verify(t.Context())
			require.NoError(t, err)
			require.Len(t, rep.Problems, 1, "%v", rep.Problems)

			assert.Equal(t, tt.wantCode, rep.Problems[0].Code)
			assert.Equal(t, tt.wantObject, rep.Problems[0].Object)
			assert.Equal(t, tt.wantChecked, rep.Checked)
			assert.Equal(t, s3publish.Requests{Get: tt.wantGets}, rep.Requests)
			assert.Len(
				t,
				fake.Calls(),
				tt.wantGets,
				"an entry that is read costs one GET, a rejected manifest only the manifest",
			)
		})
	}
}

// classificationRow is one row of TestVerifyClassificationTable.
type classificationRow struct {
	mutate      func(entries []controlplane.ManifestObject)
	config      func(*s3publish.Config)
	edit        func(manifest string) string
	name        string
	wantCode    string
	wantObject  string
	raw         []byte
	wantChecked int
	wantGets    int
}
