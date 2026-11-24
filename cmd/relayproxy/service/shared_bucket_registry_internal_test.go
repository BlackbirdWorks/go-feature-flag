package service

// Whitebox: sharedBucketRegistry is unexported, and its tests need the registry internals (the
// group count, the pending retrievers, the factory) that the exported API does not expose. The
// fakes of this file are shared by the whitebox tests of semaphore_client.go and
// flagset_manager.go.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
	"github.com/blackbirdworks/semaphore/retriever/s3retrieverv2"
)

// fakeS3 serves the same flag file for every key and counts the requests.
type fakeS3 struct {
	content []byte
	listErr error
	keys    []string
	lists   atomic.Int32
	gets    atomic.Int32

	mu        sync.Mutex
	etags     map[string]string // key -> ETag, "x" by default
	getsByKey map[string]int
}

func (f *fakeS3) setETag(key, etag string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.etags == nil {
		f.etags = map[string]string{}
	}
	f.etags[key] = etag
}

func (f *fakeS3) etagOf(key string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e, ok := f.etags[key]; ok {
		return e
	}

	return "x"
}

func (f *fakeS3) getCount(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.getsByKey[key]
}

func (f *fakeS3) ListObjectsV2(
	_ context.Context, in *s3.ListObjectsV2Input, _ ...func(*s3.Options),
) (*s3.ListObjectsV2Output, error) {
	f.lists.Add(1)
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := &s3.ListObjectsV2Output{}
	keys := f.keys
	if keys == nil {
		keys = []string{"p/a.yaml", "p/b.yaml", "p/c.yaml"}
	}
	for _, k := range keys {
		if strings.HasPrefix(k, aws.ToString(in.Prefix)) {
			out.Contents = append(out.Contents, s3types.Object{Key: aws.String(k), ETag: aws.String(f.etagOf(k))})
		}
	}

	return out, nil
}

func (f *fakeS3) DownloadObject(
	_ context.Context, in *transfermanager.DownloadObjectInput, _ ...func(*transfermanager.Options),
) (*transfermanager.DownloadObjectOutput, error) {
	f.gets.Add(1)
	f.mu.Lock()
	if f.getsByKey == nil {
		f.getsByKey = map[string]int{}
	}
	f.getsByKey[aws.ToString(in.Key)]++
	f.mu.Unlock()
	if _, err := in.WriterAt.WriteAt(f.content, 0); err != nil {
		return nil, err
	}

	return &transfermanager.DownloadObjectOutput{ETag: aws.String(f.etagOf(aws.ToString(in.Key)))}, nil
}

func newFakeRegistry(t *testing.T, f *fakeS3, created *atomic.Int32) *sharedBucketRegistry {
	t.Helper()

	return newFakeRegistryWithPolling(t, f, created, 0, false, nil)
}

func newFakeRegistryWithPolling(
	t *testing.T, f *fakeS3, created *atomic.Int32, interval time.Duration, jitter bool, logger *zap.Logger,
) *sharedBucketRegistry {
	t.Helper()

	return newSharedBucketRegistry(func(ctx context.Context, bucket string) (*s3retrieverv2.SharedItems, error) {
		created.Add(1)

		return s3retrieverv2.NewSharedItems(ctx, s3retrieverv2.SharedItemsOptions{
			Bucket: bucket, Lister: f, Downloader: f,
		})
	}, interval, jitter, logger)
}

func s3FlagSet(name, bucket, item string) config.FlagSet {
	return config.FlagSet{
		Name:    name,
		APIKeys: []string{name + "-key"},
		Retriever: &retrieverconf.RetrieverConf{
			Kind:   retrieverconf.S3Retriever,
			Bucket: bucket,
			Item:   item,
		},
		EnableSharedPollerPerBucket: true,
		PollingInterval:             60000,
	}
}

// groupCount is the number of live groups of the registry.
func (r *sharedBucketRegistry) groupCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.groups)
}

func Test_sharedBucketRegistry_retrieverFor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// run executes the scenario and returns what it observed: the groups created and the groups alive at each
		// checkpoint, compared with want.
		run  func(t *testing.T) []int
		want []int
	}{
		{name: "same bucket shares a group, other bucket has its own", run: func(t *testing.T) []int {
			var created atomic.Int32
			reg := newFakeRegistry(t, &fakeS3{}, &created)
			fs1, fs2, fs3 := s3FlagSet("a", "b1", "p/a.yaml"), s3FlagSet("b", "b1", "p/b.yaml"),
				s3FlagSet("c", "b2", "p/c.yaml")
			r1, err := reg.retrieverFor(&fs1)
			require.NoError(t, err)
			_, err = reg.retrieverFor(&fs2)
			require.NoError(t, err)
			_, err = reg.retrieverFor(&fs3)
			require.NoError(t, err)
			got := make([]int, 0, 3)
			got = append(got, int(created.Load()), reg.groupCount())

			// Shutdown twice is harmless
			require.NoError(t, r1.(*releasingRetriever).Shutdown(t.Context()))
			require.NoError(t, r1.(*releasingRetriever).Shutdown(t.Context()))

			return append(got, reg.groupCount())
		}, want: []int{2, 2, 2}},
		{name: "group is dropped after the last shutdown", run: func(t *testing.T) []int {
			reg := newFakeRegistry(t, &fakeS3{}, &atomic.Int32{})
			fs1, fs2 := s3FlagSet("a", "b1", "p/a.yaml"), s3FlagSet("b", "b1", "p/b.yaml")
			r1, err := reg.retrieverFor(&fs1)
			require.NoError(t, err)
			r2, err := reg.retrieverFor(&fs2)
			require.NoError(t, err)
			require.NoError(t, r1.(*releasingRetriever).Shutdown(t.Context()))
			got := make([]int, 0, 2)
			got = append(got, reg.groupCount())
			require.NoError(t, r2.(*releasingRetriever).Shutdown(t.Context()))

			return append(got, reg.groupCount())
		}, want: []int{1, 0}},
		{name: "unsupported configurations", run: func(t *testing.T) []int {
			reg := newFakeRegistry(t, &fakeS3{}, &atomic.Int32{})
			folder := s3FlagSet("a", "b1", "")
			other := s3FlagSet("a", "b1", "x")
			other.Retriever.Kind = retrieverconf.HTTPRetriever
			multi := s3FlagSet("a", "b1", "x")
			multi.Retrievers = &[]retrieverconf.RetrieverConf{*multi.Retriever}
			for _, fs := range []config.FlagSet{folder, other, multi} {
				_, err := reg.retrieverFor(&fs)
				require.Error(t, err)
			}

			return []int{reg.groupCount()}
		}, want: []int{0}},
		{name: "factory error", run: func(t *testing.T) []int {
			fs := s3FlagSet("a", "b1", "p/a.yaml")
			reg := failingRegistry(nil)
			_, err := reg.retrieverFor(&fs)
			require.ErrorContains(t, err, "no AWS")

			return []int{reg.groupCount()}
		}, want: []int{0}},
		{name: "concurrent callers build the group once", run: func(t *testing.T) []int {
			const callers = 4
			var building sync.WaitGroup
			building.Add(callers)
			reg := newSharedBucketRegistry(
				func(ctx context.Context, bucket string) (*s3retrieverv2.SharedItems, error) {
					// every caller is building its own group at the same time: only one is kept
					building.Done()
					building.Wait()

					return s3retrieverv2.NewSharedItems(ctx, s3retrieverv2.SharedItemsOptions{
						Bucket: bucket, Lister: &fakeS3{}, Downloader: &fakeS3{},
					})
				},
				0,
				false,
				nil,
			)
			var wg sync.WaitGroup
			retrievers := make(chan *releasingRetriever, callers)
			for i := range callers {
				wg.Go(func() {
					fs := s3FlagSet(fmt.Sprintf("f%d", i), "b1", fmt.Sprintf("p/%d.yaml", i))
					r, err := reg.retrieverFor(&fs)
					assert.NoError(t, err)
					retrievers <- r.(*releasingRetriever)
				})
			}
			wg.Wait()
			close(retrievers)
			got := make([]int, 0, 2)
			got = append(got, reg.groupCount())
			for r := range retrievers {
				require.NoError(t, r.Shutdown(t.Context()))
			}

			// all the references were taken on the same group: it is dropped by the last shutdown
			return append(got, reg.groupCount())
		}, want: []int{1, 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.run(t))
		})
	}
}

func Test_sharedBucketRegistry_preRegister(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// run executes the scenario and returns the groups created and alive at each checkpoint
		run  func(t *testing.T) []int
		want []int
	}{
		{name: "claimed retrievers are the pre-registered ones, the unclaimed ones are released",
			run: func(t *testing.T) []int {
				var created atomic.Int32
				reg := newFakeRegistry(t, &fakeS3{}, &created)
				fs1, fs2 := s3FlagSet("a", "b1", "p/a.yaml"), s3FlagSet("b", "b1", "q/b.yaml")
				require.NoError(t, reg.preRegister(&fs1))
				require.NoError(t, reg.preRegister(&fs2))
				got := make([]int, 0, 3)
				got = append(got, int(created.Load()))

				// no additional reference is taken when a pre-registered retriever is claimed
				r1, err := reg.retrieverFor(&fs1)
				require.NoError(t, err)
				require.NoError(t, r1.(*releasingRetriever).Shutdown(t.Context()))
				got = append(got, reg.groupCount()) // fs2 is still pending

				reg.releasePending()

				return append(got, reg.groupCount())
			}, want: []int{1, 1, 0}},
		{name: "invalid configuration", run: func(t *testing.T) []int {
			reg := newFakeRegistry(t, &fakeS3{}, &atomic.Int32{})
			bad := s3FlagSet("c", "b1", "")
			require.Error(t, reg.preRegister(&bad))

			return []int{reg.groupCount()}
		}, want: []int{0}},
		{name: "factory error", run: func(t *testing.T) []int {
			fs := s3FlagSet("a", "b1", "p/a.yaml")
			reg := failingRegistry(nil)
			require.ErrorContains(t, reg.preRegister(&fs), "no AWS")

			return []int{reg.groupCount()}
		}, want: []int{0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.run(t))
		})
	}
}

func failingRegistry(logger *zap.Logger) *sharedBucketRegistry {
	return newSharedBucketRegistry(func(context.Context, string) (*s3retrieverv2.SharedItems, error) {
		return nil, errors.New("no AWS")
	}, 0, false, logger)
}

func Test_sharedBucketRegistry_preRegisterAll(t *testing.T) {
	t.Parallel()
	valid := s3FlagSet("valid", "b1", "p/a.yaml")
	plain := s3FlagSet("plain", "b1", "p/b.yaml")
	plain.EnableSharedPollerPerBucket = false
	tests := []struct {
		name       string
		reg        func(logger *zap.Logger) *sharedBucketRegistry
		wantGroups int
		wantWarn   int
	}{
		{
			name: "valid flagsets are registered, others skipped",
			reg: func(l *zap.Logger) *sharedBucketRegistry {
				return newFakeRegistryWithPolling(t, &fakeS3{}, &atomic.Int32{}, 0, false, l)
			},
			wantGroups: 1,
		},
		{name: "factory failure is logged", reg: failingRegistry, wantWarn: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			core, logs := observer.New(zap.WarnLevel)
			reg := tt.reg(zap.New(core))
			reg.preRegisterAll([]config.FlagSet{valid, plain})
			assert.Equal(t, tt.wantGroups, reg.groupCount())
			assert.Equal(t, tt.wantWarn, logs.Len())
		})
	}
}

// Not parallel: the subtests use t.Setenv.
//
//nolint:paralleltest,tparallel // t.Setenv: the AWS SDK default config chain (third-party code) reads the AWS_* variables from the process environment
func Test_defaultSharedItemsFactory(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr assert.ErrorAssertionFunc
	}{
		{name: "default AWS config", wantErr: assert.NoError},
		{
			name: "invalid AWS profile",
			env: map[string]string{
				"AWS_PROFILE":                 "this-profile-does-not-exist",
				"AWS_CONFIG_FILE":             "/nonexistent/config",
				"AWS_SHARED_CREDENTIALS_FILE": "/nonexistent/credentials",
			},
			wantErr: assert.Error,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			_, err := defaultSharedItemsFactory(t.Context(), "bucket")
			tt.wantErr(t, err)
		})
	}
}

func Test_sharedBucketRegistry_warnPollingMismatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		parent      time.Duration
		parentJit   bool
		polling     int
		jitter      bool
		wantMessage []string
	}{
		{name: "unset values follow the parent", parent: 10 * time.Second},
		{name: "same values", parent: time.Minute, polling: 60000},
		{name: "same interval as the default parent", polling: 60000},
		{
			name: "different interval", parent: 10 * time.Second, polling: 5000,
			wantMessage: []string{"the pollingInterval of the flagset is ignored"},
		},
		{
			name:        "jitter only on the flagset",
			jitter:      true,
			wantMessage: []string{"the enablePollingJitter of the flagset is ignored"},
		},
		{name: "jitter on the parent", parentJit: true, jitter: true},
		{
			name: "both differ", parent: 10 * time.Second, polling: 1000, jitter: true,
			wantMessage: []string{"the pollingInterval of the flagset", "the enablePollingJitter of the flagset"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			core, logs := observer.New(zap.WarnLevel)
			reg := newFakeRegistryWithPolling(t, &fakeS3{}, &atomic.Int32{}, tt.parent, tt.parentJit, zap.New(core))
			fs := s3FlagSet("fs", "b1", "p/a.yaml")
			fs.PollingInterval, fs.EnablePollingJitter = tt.polling, tt.jitter
			r, err := reg.retrieverFor(&fs)
			require.NoError(t, err)
			require.NoError(t, r.(*releasingRetriever).Shutdown(t.Context()))

			require.Equal(t, len(tt.wantMessage), logs.Len())
			for i, snippet := range tt.wantMessage {
				entry := logs.All()[i]
				assert.Contains(t, entry.Message, snippet)
				assert.Equal(t, "fs", entry.ContextMap()["flagset"])
			}
		})
	}
}
