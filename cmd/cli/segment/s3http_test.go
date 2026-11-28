package segment_test

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin/admintest"
)

// s3Server is a small S3 over HTTP: path style, HEAD, GET with If-Match and Content-Encoding, ListObjectsV2 in pages
// of two. The commands reach it through the real connection factory and the AWS SDK.
type s3Server struct {
	objs   map[string][]byte
	onGet  func(key string)
	srv    *httptest.Server
	listed []string
	mu     sync.Mutex
}

func newS3Server(t *testing.T) *s3Server {
	t.Helper()

	srv := &s3Server{objs: map[string][]byte{}}
	srv.srv = httptest.NewServer(srv)
	t.Cleanup(srv.srv.Close)

	return srv
}

func etagOf(data []byte) string {
	sum := sha256.Sum256(data)

	return `"` + hex.EncodeToString(sum[:8]) + `"`
}

func s3Error(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<?xml version="1.0"?><Error><Code>%s</Code><Message>%s</Message></Error>`, code, code)
}

type listResult struct {
	XMLName     xml.Name     `xml:"ListBucketResult"`
	NextToken   string       `xml:"NextContinuationToken,omitempty"`
	Contents    []listObject `xml:"Contents"`
	IsTruncated bool         `xml:"IsTruncated"`
}

type listObject struct {
	Key  string `xml:"Key"`
	ETag string `xml:"ETag"`
	Size int    `xml:"Size"`
}

func (s *s3Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/bkt/")

	if r.URL.Query().Get("list-type") == "2" {
		s.list(w, r)

		return
	}

	if r.Method == http.MethodGet && s.onGet != nil {
		s.onGet(key)
	}

	s.mu.Lock()
	data, ok := s.objs[key]
	s.mu.Unlock()

	switch {
	case !ok:
		s3Error(w, http.StatusNotFound, "NoSuchKey")

		return
	case r.Header.Get("If-Match") != "" && r.Header.Get("If-Match") != etagOf(data):
		s3Error(w, http.StatusPreconditionFailed, "PreconditionFailed")

		return
	}

	w.Header().Set("ETag", etagOf(data))
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))

	if bytes.HasPrefix(data, []byte{0x1f, 0x8b}) {
		w.Header().Set("Content-Encoding", "gzip")
	}

	if r.Method == http.MethodGet {
		_, _ = w.Write(data)
	}
}

func (s *s3Server) put(key string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.objs[key] = data
}

func (s *s3Server) list(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.listed = append(s.listed, r.URL.Query().Get("prefix"))

	after := r.URL.Query().Get("continuation-token")
	out := listResult{}

	for _, key := range slices.Sorted(maps.Keys(s.objs)) {
		if !strings.HasPrefix(key, r.URL.Query().Get("prefix")) || key <= after {
			continue
		}

		if len(out.Contents) == 2 {
			out.IsTruncated, out.NextToken = true, out.Contents[1].Key

			break
		}

		out.Contents = append(out.Contents, listObject{Key: key, ETag: etagOf(s.objs[key]), Size: len(s.objs[key])})
	}

	w.Header().Set("Content-Type", "application/xml")
	_ = xml.NewEncoder(w).Encode(out)
}

func (s *s3Server) bootstrap(plane bool) string {
	doc := `version: 2
connections:
  fast: {type: valkey, addresses: ["127.0.0.1:6380"]}
  src:
    type: s3
    region: us-east-1
    bucket: bkt
    endpoint: ` + s.srv.URL + `
    pathStyle: true
    credentials: {type: static, accessKeyId: {fromEnv: K}, secretAccessKey: {fromEnv: K}}
`
	if plane {
		doc += "controlPlane: {connection: src, manifest: {key: manifest.json}}\n"
	}

	return doc
}

func gzipped(t *testing.T, text string) []byte {
	t.Helper()

	var buf bytes.Buffer

	zw := gzip.NewWriter(&buf)
	_, err := zw.Write([]byte(text))
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	return buf.Bytes()
}

// TestRun_s3OverHTTP runs the commands against a server that speaks S3: the SDK, the connection factory and the
// adapter are the real ones.
func TestRun_s3OverHTTP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		prepare     func(t *testing.T, srv *s3Server)
		wantErr     error
		name        string
		from        string
		wantMembers []string
	}{
		{
			name: "an object", from: "s3://bkt/ids/a.txt", wantMembers: []string{"a", "b"},
		},
		{
			name:        "a prefix over two pages with a gzip object",
			from:        "s3://bkt/ids/",
			wantMembers: []string{"a", "b", "c", "d", "e"},
		},
		{
			name: "an object that was replaced after the listing", from: "s3://bkt/ids/",
			prepare: func(_ *testing.T, srv *s3Server) {
				srv.onGet = func(key string) {
					if key == "ids/c.txt.gz" {
						srv.put("ids/e.txt", []byte("zzz\n"))
					}
				}
			},
			wantErr: segmentadmin.ErrObjectChanged,
		},
		{name: "a missing object", from: "s3://bkt/ids/none.txt", wantErr: segmentadmin.ErrObjectMissing},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := newS3Server(t)
			srv.put("ids/a.txt", []byte("a\nb\n"))
			srv.put("ids/b.txt", []byte("c"))
			srv.put("ids/c.txt.gz", gzipped(t, "d\n"))
			srv.put("ids/e.txt", []byte("e\n"))

			if tt.prepare != nil {
				tt.prepare(t, srv)
			}

			store := admintest.New()
			all := map[string]string{fileS3: srv.bootstrap(false), segmentsFile: segmentsDoc}

			args := append([]string{cmdImport, flagFrom, tt.from}, s3Common(fileS3)...)

			got := runS3Real(t, store, all, args...)

			if tt.wantErr != nil {
				require.ErrorIs(t, got.err, tt.wantErr)
				assert.Empty(t, store.Members(liveKey))

				return
			}

			require.NoError(t, got.err)
			assert.Equal(t, tt.wantMembers, store.Members(liveKey))
		})
	}
}

// TestRun_planeOverS3 reads the manifest and its segments object from S3, and resolves the segment from them.
func TestRun_planeOverS3(t *testing.T) {
	t.Parallel()

	srv := newS3Server(t)
	srv.put("ids/a.txt", []byte("a\nb\n"))

	doc := []byte(segmentsDoc)
	manifest := controlplane.Manifest{
		APIVersion: controlplane.APIVersion, Kind: "Manifest", Channel: "stable", Generation: 1,
		Objects: []controlplane.ManifestObject{{
			Name: "segs", Kind: controlplane.KindSegments, Connection: "src", Key: "segs.yaml", Format: "yaml",
			Checksum: controlplane.ChecksumOf(doc), Size: int64(len(doc)),
		}},
	}

	raw, err := json.Marshal(&manifest)
	require.NoError(t, err)

	srv.put("manifest.json", raw)
	srv.put("segs.yaml", doc)

	store := admintest.New()
	all := map[string]string{fileS3: srv.bootstrap(true)}
	args := []string{cmdImport, flagFrom, "s3://bkt/ids/a.txt", flagConfig, fileS3, flagSegment, segVIP}

	got := runS3Real(t, store, all, args...)

	require.NoError(t, got.err)
	assert.Equal(t, []string{"a", "b"}, store.Members(liveKey))
	assert.Equal(t, helper.ExitFailure, helper.ExitCodeOf(runS3Real(t, store, all, args[:len(args)-1]...).err))
}
