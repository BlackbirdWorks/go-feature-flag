package s3retrieverv2_test

import (
	"bytes"
	"errors"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/cache"
	"github.com/blackbirdworks/semaphore/modules/core/dto"
	"github.com/blackbirdworks/semaphore/retriever"
	"github.com/blackbirdworks/semaphore/retriever/s3retrieverv2"
	"github.com/blackbirdworks/semaphore/testutils"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

var (
	errBoom   = errors.New("boom")
	errKaboom = errors.New("kaboom")
)

const (
	yamlA = "flagA:\n  variations:\n    on: true\n    off: false\n  defaultRule:\n    variation: on\n"
	yamlB = "flagB:\n  variations:\n    on: true\n    off: false\n  defaultRule:\n    variation: off\n"
	jsonC = `{"flagC": {"variations": {"on": true, "off": false}, "defaultRule": {"variation": "on"}}}`
	tomlD = "[flagD]\n[flagD.variations]\non = true\noff = false\n[flagD.defaultRule]\nvariation = \"on\"\n"
)

// cfg holds the settings of a retriever (Retriever contains a mutex, so it can't be copied).
type cfg struct {
	Item      string
	Prefix    string
	Pattern   string
	Recursive bool
	// noLister removes the lister after Init, so the folder mode finds none.
	noLister bool
}

func (c cfg) build() *s3retrieverv2.Retriever {
	return &s3retrieverv2.Retriever{
		Bucket: "b", Item: c.Item, Prefix: c.Prefix, Pattern: c.Pattern, Recursive: c.Recursive,
	}
}

type retrieveCase struct {
	name  string
	setup func(m *testutils.S3PrefixMock)
	r     cfg
	steps []step
}

// step is one Retrieve call (or several concurrent ones), preceded by a mutation of the bucket.
type step struct {
	mutate         func(m *testutils.S3PrefixMock)
	wantDownloads  map[string]int // cumulative number of downloads per key
	wantLog        string         // substring expected in the logs
	wantNoLog      string         // substring not expected in the logs
	wantLogCountOf string         // substring counted by wantLogCount
	wantErr        string
	wantFlags      []string // flag names (prefix mode: parsed from JSON; item mode: substrings of the raw file)
	wantContains   []string // substrings expected in the raw output
	// wantSameAs lists local files whose item-mode decoding (merged) must equal the decoded result.
	wantSameAs   []string
	concurrent   int // number of concurrent Retrieve calls, 0 means one call
	wantLogCount int // number of times wantLogCountOf is expected in the logs so far (all steps included)
	nilCtx       bool
}

func TestRetriever_Retrieve(t *testing.T) {
	t.Parallel()

	tests := slices.Concat([]retrieveCase{
		{
			name:  "item mode unchanged",
			setup: func(m *testutils.S3PrefixMock) { m.Put("flags/a.yaml", yamlA) },
			r:     cfg{Item: "flags/a.yaml"},
			steps: []step{
				{wantFlags: []string{"flagA"}},
				// item mode has no ETag cache: every call downloads
				{wantFlags: []string{"flagA"}, nilCtx: true, wantDownloads: map[string]int{"flags/a.yaml": 2}},
			},
		},
		{
			name:  "item mode download error",
			setup: func(_ *testutils.S3PrefixMock) {},
			r:     cfg{Item: "missing.yaml"},
			steps: []step{{wantErr: "unable to download item from S3 \"missing.yaml\""}},
		},
		{
			name:  "not initialized",
			setup: func(_ *testutils.S3PrefixMock) {},
			r:     cfg{Prefix: "f/", noLister: true},
			steps: []step{{wantErr: "lister is not initialized"}},
		},
		{
			name: "prefix non recursive",
			setup: func(m *testutils.S3PrefixMock) {
				m.Put("flags/a.yaml", yamlA)
				m.Put("flags/b.yaml", yamlB)
				m.Put("flags/sub/c.yaml", "flagC: {}\n")
				m.Put("flags/", "") // folder marker
				m.Put("other/d.yaml", "flagD: {}\n")
			},
			r:     cfg{Prefix: "flags/"},
			steps: []step{{wantFlags: []string{"flagA", "flagB"}}},
		},
		{
			name: "prefix without trailing slash non recursive",
			setup: func(m *testutils.S3PrefixMock) {
				m.Put("flags/a.yaml", yamlA)
				m.Put("flags/sub/c.yaml", "flagC: {}\n")
				m.Put("flags-old/z.yaml", "flagZ: {}\n")
			},
			r:     cfg{Prefix: "flags"},
			steps: []step{{wantFlags: []string{"flagA"}}},
		},
		{
			name: "prefix with a leading and trailing slash is the same folder",
			setup: func(m *testutils.S3PrefixMock) {
				m.Put("flags/a.yaml", yamlA)
				m.Put("flags/sub/c.yaml", "flagC: {}\n")
				m.Put("flags-old/z.yaml", "flagZ: {}\n")
			},
			r:     cfg{Prefix: "/flags/"},
			steps: []step{{wantFlags: []string{"flagA"}}},
		},
		{
			name: "prefix with a trailing slash is the same folder",
			setup: func(m *testutils.S3PrefixMock) {
				m.Put("flags/a.yaml", yamlA)
				m.Put("flags/sub/c.yaml", "flagC: {}\n")
				m.Put("flags-old/z.yaml", "flagZ: {}\n")
			},
			r:     cfg{Prefix: "flags/"},
			steps: []step{{wantFlags: []string{"flagA"}}},
		},
		{
			name: "no item and no prefix reads the root, non recursive",
			setup: func(m *testutils.S3PrefixMock) {
				m.Put("a.yaml", yamlA)
				m.Put("flags/b.yaml", yamlB)
			},
			r:     cfg{},
			steps: []step{{wantFlags: []string{"flagA"}}},
		},
		{
			name: "no item and no prefix reads the whole bucket when recursive",
			setup: func(m *testutils.S3PrefixMock) {
				m.Put("a.yaml", yamlA)
				m.Put("flags/b.yaml", yamlB)
			},
			r:     cfg{Recursive: true},
			steps: []step{{wantFlags: []string{"flagA", "flagB"}}},
		},
		{
			name: "pattern filter at the root",
			setup: func(m *testutils.S3PrefixMock) {
				m.Put("a.yaml", yamlA)
				m.Put("b.yml", yamlB)
				m.Put("notes.txt", "x")
			},
			r:     cfg{Pattern: "*.yaml"},
			steps: []step{{wantFlags: []string{"flagA"}}},
		},
		{
			name: "root prefix non recursive",
			setup: func(m *testutils.S3PrefixMock) {
				m.Put("a.yaml", yamlA)
				m.Put("flags/b.yaml", yamlB)
			},
			r:     cfg{Prefix: "/"},
			steps: []step{{wantFlags: []string{"flagA"}}},
		},
		{
			name: "root prefix recursive",
			setup: func(m *testutils.S3PrefixMock) {
				m.Put("a.yaml", yamlA)
				m.Put("flags/b.yaml", yamlB)
				m.Put("flags/deep/c.yaml", "flagC: {}\n")
			},
			r:     cfg{Prefix: "/", Recursive: true},
			steps: []step{{wantFlags: []string{"flagA", "flagB", "flagC"}}},
		},
		{
			name: "leading slash is trimmed",
			setup: func(m *testutils.S3PrefixMock) {
				m.Put("a.yaml", yamlA)
				m.Put("flags/b.yaml", yamlB)
			},
			r:     cfg{Prefix: "/flags/"},
			steps: []step{{wantFlags: []string{"flagB"}}},
		},
		{
			name: "prefix recursive",
			setup: func(m *testutils.S3PrefixMock) {
				m.Put("flags/a.yaml", yamlA)
				m.Put("flags/sub/b.yml", yamlB)
				m.Put("flags/sub/deep/", "")
				m.Put("other/d.yaml", "flagD: {}\n")
			},
			r:     cfg{Prefix: "flags/", Recursive: true},
			steps: []step{{wantFlags: []string{"flagA", "flagB"}}},
		},
		{
			name: "pattern filter",
			setup: func(m *testutils.S3PrefixMock) {
				m.Put("flags/a.yaml", yamlA)
				m.Put("flags/notes.txt", "not a flag file")
				m.Put("flags/sub/b.yaml", yamlB)
			},
			r:     cfg{Prefix: "flags/", Pattern: "*.yaml", Recursive: true},
			steps: []step{{wantFlags: []string{"flagA", "flagB"}}},
		},
		{
			name: "pagination",
			setup: func(mockS3 *testutils.S3PrefixMock) {
				mockS3.PageSize = 2
				mockS3.Put("f/a.yaml", yamlA)
				mockS3.Put("f/b.yaml", yamlB)
				mockS3.Put("f/c.yaml", "flagC: {}\n")
				mockS3.Put("f/d.yaml", "flagD: {}\n")
				mockS3.Put("f/e.yaml", "flagE: {}\n")
			},
			r: cfg{Prefix: "f/"},
			steps: []step{
				{wantFlags: []string{"flagA", "flagB", "flagC", "flagD", "flagE"}},
			},
		},
		{
			name:  "list error",
			setup: func(m *testutils.S3PrefixMock) { m.ListErr = errBoom },
			r:     cfg{Prefix: "f/"},
			steps: []step{
				{wantErr: `unable to list objects from S3 with prefix "f/", store: listing a page of objects: boom`},
			},
		},
		{
			name: "download error",
			setup: func(m *testutils.S3PrefixMock) {
				m.Put("f/a.yaml", yamlA)
				m.DownloadErr = errKaboom
			},
			r:     cfg{Prefix: "f/"},
			steps: []step{{wantErr: `unable to download item from S3 "f/a.yaml", S3 downloader: kaboom`}},
		},
		{
			name: "concurrent calls are serialized",
			setup: func(m *testutils.S3PrefixMock) {
				m.Put("f/a.yaml", yamlA)
				m.Put("f/b.yaml", yamlB)
			},
			r: cfg{Prefix: "f/"},
			steps: []step{{
				concurrent:    20,
				wantFlags:     []string{"flagA", "flagB"},
				wantDownloads: map[string]int{"f/a.yaml": 1, "f/b.yaml": 1},
			}},
		},
		{
			// the object is replaced between the list and the download: the cache must hold the ETag
			// of the downloaded content, so that the next list (A again) triggers a new download
			name: "object replaced between list and download is downloaded again on the next poll",
			setup: func(mockS3 *testutils.S3PrefixMock) {
				mockS3.Put("f/a.yaml", yamlA)

				swapped := false
				mockS3.BeforeDownload = func(key string) {
					if !swapped {
						swapped = true

						mockS3.Put(key, yamlB)
					}
				}
			},
			r: cfg{Prefix: "f/"},
			steps: []step{
				{wantFlags: []string{"flagB"}},
				{
					mutate:        func(m *testutils.S3PrefixMock) { m.Put("f/a.yaml", yamlA) },
					wantFlags:     []string{"flagA"},
					wantDownloads: map[string]int{"f/a.yaml": 2},
				},
			},
		},
		{
			name: "no etag never hits the cache",
			setup: func(m *testutils.S3PrefixMock) {
				m.NoETag = true
				m.Put("f/a.yaml", yamlA)
			},
			r: cfg{Prefix: "f/"},
			steps: []step{
				{wantFlags: []string{"flagA"}, wantDownloads: map[string]int{"f/a.yaml": 1}},
				{wantFlags: []string{"flagA"}, wantDownloads: map[string]int{"f/a.yaml": 2}},
				{
					mutate:        func(m *testutils.S3PrefixMock) { m.Put("f/a.yaml", yamlB) },
					wantFlags:     []string{"flagB"},
					wantDownloads: map[string]int{"f/a.yaml": 3},
				},
			},
		},
	}, roundTripCases(t))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			runRetrieveCase(t, tt.setup, tt.r, tt.steps)
		})
	}
}

// roundTripCases checks that the merged output, decoded like the flag manager does, is the same
// as the decoding of the original file in item mode.
func roundTripCases(t *testing.T) []retrieveCase {
	t.Helper()

	files := []string{
		"flag-config.yaml", "flag-config.json", "flag-config.toml",
		"flag-config-multiline-query.json", "flag-config-scheduled-v0.yaml",
		"flag-config-3rd-file.yaml", "flag-config-custom-bucketingkey.yaml",
	}

	cases := make([]retrieveCase, 0, len(files))
	for _, fileName := range files {
		local := "../../testdata/" + fileName
		content, err := os.ReadFile(local)
		require.NoError(t, err)

		cases = append(cases, retrieveCase{
			name:  "round trip " + fileName,
			setup: func(m *testutils.S3PrefixMock) { m.Put("f/"+fileName, string(content)) },
			r:     cfg{Prefix: "f/"},
			steps: []step{{wantSameAs: []string{local}}},
		})
	}

	return cases
}

func runRetrieveCase(t *testing.T, setup func(*testutils.S3PrefixMock), conf cfg, steps []step) {
	t.Helper()

	mockS3 := &testutils.S3PrefixMock{}
	setup(mockS3)

	r := conf.build()
	r.SetDownloader(mockS3)
	r.SetLister(mockS3)

	logs := &bytes.Buffer{}
	logger := &fflog.FFLogger{LeveledLogger: slog.New(slog.NewTextHandler(logs, nil))}
	require.NoError(t, r.Init(t.Context(), logger))

	if conf.noLister {
		r.SetLister(nil)
	}

	t.Cleanup(func() { _ = r.Shutdown(t.Context()) })

	for i, currStep := range steps {
		if currStep.mutate != nil {
			currStep.mutate(mockS3)
		}

		results, errs := retrieveN(t, r, currStep)
		for idx := range results {
			checkResult(t, r, idx, currStep, results[idx], errs[idx], i)
		}

		for k, n := range currStep.wantDownloads {
			assert.Equal(t, n, mockS3.DownloadCalls(k), "downloads of %s at step %d", k, i)
		}

		if currStep.wantLog != "" {
			assert.Contains(t, logs.String(), currStep.wantLog, "step %d", i)
		}

		if currStep.wantNoLog != "" {
			assert.NotContains(t, logs.String(), currStep.wantNoLog, "step %d", i)
		}

		if currStep.wantLogCountOf != "" {
			assert.Equal(t, currStep.wantLogCount, strings.Count(logs.String(), currStep.wantLogCountOf), "step %d", i)
		}
	}
}

// retrieveN calls Retrieve currStep.concurrent times (at least once) at the same time.
func retrieveN(t *testing.T, r *s3retrieverv2.Retriever, currStep step) ([][]byte, []error) {
	t.Helper()

	calls := max(currStep.concurrent, 1)

	var wg sync.WaitGroup

	results := make([][]byte, calls)

	errs := make([]error, calls)
	for callIdx := range calls {
		wg.Go(func() {
			if currStep.nilCtx {
				//nolint:staticcheck // SA1012: Retrieve accepts nil context defensively; test covers that behavior
				results[callIdx], errs[callIdx] = r.Retrieve(nil)

				return
			}

			results[callIdx], errs[callIdx] = r.Retrieve(t.Context())
		})
	}

	wg.Wait()

	return results, errs
}

func checkResult(t *testing.T, r *s3retrieverv2.Retriever, _ int, currStep step, got []byte, err error, i int) {
	t.Helper()

	if currStep.wantErr != "" {
		require.ErrorContains(t, err, currStep.wantErr, "step %d", i)

		return
	}

	require.NoError(t, err, "step %d", i)

	if r.Item != "" {
		// item mode returns the raw file
		for _, f := range currStep.wantFlags {
			assert.Contains(t, string(got), f, "step %d", i)
		}
	} else {
		checkPrefixOutput(t, r.OutputFormat(), currStep, got, i)
	}

	for _, c := range currStep.wantContains {
		assert.Contains(t, string(got), c, "step %d", i)
	}
}

func checkPrefixOutput(t *testing.T, format string, currStep step, got []byte, i int) {
	t.Helper()

	flags, err := cache.ConvertToFlagStruct(got, format)
	require.NoError(t, err, "step %d", i)

	if currStep.wantFlags != nil {
		keys := make([]string, 0, len(flags))
		for k := range flags {
			keys = append(keys, k)
		}

		assert.ElementsMatch(t, currStep.wantFlags, keys, "step %d", i)
	}

	if len(currStep.wantSameAs) > 0 {
		want := map[string]dto.DTO{}

		for _, f := range currStep.wantSameAs {
			content, rErr := os.ReadFile(f)
			require.NoError(t, rErr)

			parsed, pErr := cache.ConvertToFlagStruct(content, strings.TrimPrefix(filepath.Ext(f), "."))
			require.NoError(t, pErr)
			maps.Copy(want, parsed)
		}

		assert.Equal(t, want, flags, "step %d", i)
	}
}

func TestRetriever_Init(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// noDownloader skips SetDownloader, so Init must build a real client.
		wantErr string
		r       cfg
		// wantAwsConfigNil: item mode with an injected downloader must not load the AWS config.
		wantAwsConfigNil bool
	}{
		{name: "item only", r: cfg{Item: "a.yaml"}, wantAwsConfigNil: true},
		{name: "prefix only", r: cfg{Prefix: "f/"}, wantAwsConfigNil: true},
		{name: "root prefix", r: cfg{Prefix: "/", Recursive: true}, wantAwsConfigNil: true},
		{name: "prefix with pattern", r: cfg{Prefix: "f/", Pattern: "*.yaml"}, wantAwsConfigNil: true},
		{
			name:    "item and prefix",
			r:       cfg{Item: "a.yaml", Prefix: "f/"},
			wantErr: "impossible to init S3 retriever v2: item and prefix are mutually exclusive",
		},
		{name: "neither item nor prefix is the bucket root", r: cfg{}, wantAwsConfigNil: true},
		{
			name:    "item with pattern",
			r:       cfg{Item: "a.yaml", Pattern: "*.yaml"},
			wantErr: "impossible to init S3 retriever v2: pattern and recursive can only be used without item",
		},
		{
			name:    "item with recursive",
			r:       cfg{Item: "a.yaml", Recursive: true},
			wantErr: "impossible to init S3 retriever v2: pattern and recursive can only be used without item",
		},
		{
			name:    "invalid pattern",
			r:       cfg{Prefix: "f/", Pattern: "[a-"},
			wantErr: `impossible to init S3 retriever v2: invalid pattern "[a-"`,
		},
		{
			name:    "pattern with a slash",
			r:       cfg{Prefix: "f/", Pattern: "sub/*.yaml"},
			wantErr: `can't contain "/"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := tt.r.build()
			m := &testutils.S3PrefixMock{}
			r.SetDownloader(m)
			r.SetLister(m)

			err := r.Init(t.Context(), nil)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				assert.Equal(t, retriever.RetrieverError, r.Status())

				return
			}

			require.NoError(t, err)
			assert.Equal(t, retriever.RetrieverReady, r.Status())

			if tt.wantAwsConfigNil {
				assert.Nil(t, r.AwsConfig)
			}
		})
	}

	t.Run("item mode with only a downloader does not need a lister", func(t *testing.T) {
		t.Parallel()

		r := cfg{Item: "a.yaml"}.build()
		r.SetDownloader(&testutils.S3PrefixMock{})
		require.NoError(t, r.Init(t.Context(), nil))
		assert.Nil(t, r.AwsConfig)
	})
}

func TestRetriever_Shutdown(t *testing.T) {
	t.Parallel()

	mockS3 := &testutils.S3PrefixMock{}
	mockS3.Put("f/a.yaml", yamlA)
	mockS3.Put("f/readme.txt", "hello")

	r := cfg{Prefix: "f/"}.build()
	logs := &bytes.Buffer{}
	logger := &fflog.FFLogger{LeveledLogger: slog.New(slog.NewTextHandler(logs, nil))}

	r.SetDownloader(mockS3)
	r.SetLister(mockS3)
	require.NoError(t, r.Init(t.Context(), logger))
	_, err := r.Retrieve(t.Context())
	require.NoError(t, err)

	require.NoError(t, r.Shutdown(t.Context()))
	assert.Equal(t, retriever.RetrieverNotReady, r.Status())
	_, err = r.Retrieve(t.Context())
	require.ErrorContains(t, err, "downloader is not initialized")
	assert.Equal(t, retriever.RetrieverError, r.Status())

	// Shutdown clears the skipped files, so a new Init warns again about them.
	r.SetDownloader(mockS3)
	r.SetLister(mockS3)
	require.NoError(t, r.Init(t.Context(), logger))
	_, err = r.Retrieve(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 2, strings.Count(logs.String(), "key=f/readme.txt"))
	assert.Contains(t, logs.String(), "S3 retriever: file skipped")
	assert.Contains(t, logs.String(), "bucket=b")
}

func TestRetriever_Init_KeepsLoader(t *testing.T) {
	t.Parallel()

	mockS3 := &testutils.S3PrefixMock{}
	mockS3.Put("f/a.yaml", yamlA)

	r := cfg{Prefix: "f/"}.build()
	r.SetDownloader(mockS3)
	r.SetLister(mockS3)
	require.NoError(t, r.Init(t.Context(), nil))
	_, err := r.Retrieve(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "yaml", r.OutputFormat())

	// a second Init keeps the cache and the last output format
	require.NoError(t, r.Init(t.Context(), nil))
	assert.Equal(t, "yaml", r.OutputFormat())
	_, err = r.Retrieve(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, mockS3.DownloadCalls("f/a.yaml"))
}

func TestRetriever_Init_ChangedSelection(t *testing.T) {
	t.Parallel()

	mock := &testutils.S3PrefixMock{}
	mock.Put("f/a.yaml", yamlA)
	mock.Put("g/b.yaml", yamlB)

	r := cfg{Prefix: "f/"}.build()
	r.SetDownloader(mock)
	r.SetLister(mock)
	require.NoError(t, r.Init(t.Context(), nil))
	got, err := r.Retrieve(t.Context())
	require.NoError(t, err)
	assert.Contains(t, string(got), "flagA")

	r.Prefix = "g/"
	require.NoError(t, r.Init(t.Context(), nil))
	got, err = r.Retrieve(t.Context())
	require.NoError(t, err)
	assert.Contains(t, string(got), "flagB")
	assert.NotContains(t, string(got), "flagA")

	// switching to single file mode drops the folder output format
	r.Prefix = ""
	r.Item = "g/b.yaml"
	require.NoError(t, r.Init(t.Context(), nil))
	assert.Empty(t, r.OutputFormat())
}

func TestRetriever_SetLister(t *testing.T) {
	t.Parallel()

	mockS3 := &testutils.S3PrefixMock{}
	mockS3.Put("f/a.yaml", yamlA)

	r := cfg{Prefix: "f/"}.build()
	r.SetDownloader(mockS3)
	r.SetLister(mockS3)
	require.NoError(t, r.Init(t.Context(), nil))
	got, err := r.Retrieve(t.Context())
	require.NoError(t, err)
	assert.Contains(t, string(got), "flagA")
	assert.Equal(t, 1, mockS3.ListCalls())
}

func TestRetriever_SetDownloader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		wantErr string
		want    string
		r       cfg
	}{
		{name: "single file works without Init", r: cfg{Item: "a.yaml"}, want: yamlA},
		{
			name:    "empty item without Init is not a single file",
			r:       cfg{},
			wantErr: "S3 retriever not initialized: call Init",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockS3 := &testutils.S3PrefixMock{}
			mockS3.Put("a.yaml", yamlA)

			r := tt.r.build()
			r.SetDownloader(mockS3)

			got, err := r.Retrieve(t.Context())
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, string(got))
			assert.Equal(t, 1, mockS3.DownloadCalls("a.yaml"))
		})
	}
}

func TestRetriever_OutputFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want string
		r    cfg
	}{
		{name: "item mode keeps the global format", r: cfg{Item: "a.yaml"}, want: ""},
		{name: "prefix mode without pattern is unknown before the first retrieve", r: cfg{Prefix: "f/"}, want: ""},
		{name: "prefix mode guesses from the pattern", r: cfg{Prefix: "f/", Pattern: "*.toml"}, want: "toml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := tt.r.build()
			m := &testutils.S3PrefixMock{}
			r.SetDownloader(m)
			r.SetLister(m)
			require.NoError(t, r.Init(t.Context(), nil))
			assert.Equal(t, tt.want, r.OutputFormat())
		})
	}
}
