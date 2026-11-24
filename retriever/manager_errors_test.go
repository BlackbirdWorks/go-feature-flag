package retriever_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/cache"
	"github.com/blackbirdworks/semaphore/internal/notification"
	"github.com/blackbirdworks/semaphore/notifier"
	"github.com/blackbirdworks/semaphore/retriever"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

const persistedFlags = `test-flag:
  variations:
    A: true
    B: false
  defaultRule:
    variation: A
`

// failingRetriever never retrieves anything.
type failingRetriever struct{}

func (failingRetriever) Retrieve(context.Context) ([]byte, error) {
	return nil, errors.New("the source is down")
}

// shutdownFailingRetriever retrieves flags but cannot be shut down.
type shutdownFailingRetriever struct{ countingRetriever }

func (*shutdownFailingRetriever) Init(context.Context, *fflog.FFLogger) error { return nil }
func (*shutdownFailingRetriever) Shutdown(context.Context) error              { return errors.New("cannot close") }
func (*shutdownFailingRetriever) Status() retriever.Status                    { return retriever.RetrieverReady }

func newErrorManager(r retriever.Retriever, conf retriever.ManagerConfig) *retriever.Manager {
	logger := fflog.FFLogger{}
	cacheManager := cache.New(notification.NewService([]notifier.Notifier{}), "", &logger)
	return retriever.NewManager(conf, []retriever.Retriever{r}, cacheManager, &logger)
}

func TestManagerInit_FirstRetrieverError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		persisted   *string // content of the persistent flag configuration file, nil when there is none
		withFile    bool
		startAnyway bool
		wantErr     string
		wantFlag    bool
	}{
		{name: "no persistent file and no tolerance fails", wantErr: "impossible to retrieve the flags"},
		{name: "starts without flags when the retriever error is tolerated", startAnyway: true},
		{name: "serves the persisted flags", withFile: true, persisted: new(persistedFlags), wantFlag: true},
		{name: "missing persistent file fails", withFile: true, wantErr: "impossible to use the persistent flag configuration file"},
		{name: "missing persistent file is tolerated", withFile: true, startAnyway: true},
		{name: "unreadable persistent content fails", withFile: true, persisted: new("{not: [yaml"),
			wantErr: "impossible to use the persistent flag configuration file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			conf := retriever.ManagerConfig{FileFormat: "yaml", StartWithRetrieverError: tt.startAnyway}
			if tt.withFile {
				conf.PersistentFlagConfigurationFile = filepath.Join(t.TempDir(), "persist.yaml")
				if tt.persisted != nil {
					require.NoError(t, os.WriteFile(conf.PersistentFlagConfigurationFile, []byte(*tt.persisted), 0o600))
				}
			}
			m := newErrorManager(failingRetriever{}, conf)
			err := m.Init(context.Background())
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			defer func() { _ = m.Shutdown(context.Background()) }()

			flags, err := m.GetFlagsFromCache(context.Background())
			readOnly, errReadOnly := m.GetFlagsFromCacheReadOnly(context.Background())
			require.NoError(t, errReadOnly)
			assert.Len(t, readOnly, len(flags))
			if tt.wantFlag {
				require.NoError(t, err)
				assert.Contains(t, flags, "test-flag")
				f, err := m.GetFlag("test-flag")
				require.NoError(t, err)
				assert.NotNil(t, f)
				assert.False(t, m.GetCacheRefreshDate().IsZero())
			} else {
				require.NoError(t, err)
				assert.Empty(t, flags, "the relay starts, every flag evaluates to its default")
			}
		})
	}
}

//go:fix inline
func ptr(s string) *string { return new(s) }

func TestManagerForceRefresh(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		retriever retriever.Retriever
		want      bool
	}{
		{name: "refreshes from a working retriever", retriever: &countingRetriever{}, want: true},
		{name: "reports a failing retriever", retriever: failingRetriever{}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := newErrorManager(tt.retriever, retriever.ManagerConfig{FileFormat: "json", StartWithRetrieverError: true})
			require.NoError(t, m.Init(context.Background()))
			defer func() { _ = m.Shutdown(context.Background()) }()
			assert.Equal(t, tt.want, m.ForceRefresh(context.Background()))
		})
	}
}

func TestManagerGetFlagsFromCacheWithoutManager(t *testing.T) {
	t.Parallel()
	var m *retriever.Manager
	_, err := m.GetFlagsFromCache(context.Background())
	assert.ErrorContains(t, err, "cache is not initialized")
	_, err = m.GetFlagsFromCacheReadOnly(context.Background())
	assert.ErrorContains(t, err, "cache is not initialized")
}

func TestManagerShutdownReportsTheRetrieversThatFail(t *testing.T) {
	t.Parallel()
	m := newErrorManager(&shutdownFailingRetriever{}, retriever.ManagerConfig{FileFormat: "json"})
	require.NoError(t, m.Init(context.Background()))
	assert.ErrorContains(t, m.Shutdown(context.Background()), "error while shutting down the retrievers")
}

func TestManagerInitFailsWhenARetrieverCannotInitialise(t *testing.T) {
	t.Parallel()
	m := newErrorManager(&initFailingRetriever{}, retriever.ManagerConfig{FileFormat: "json"})
	assert.ErrorContains(t, m.Init(context.Background()), "error while initializing the retrievers")
}

type initFailingRetriever struct{ countingRetriever }

func (*initFailingRetriever) Init(context.Context, *fflog.FFLogger) error { return errors.New("no") }
func (*initFailingRetriever) Shutdown(context.Context) error              { return nil }
func (*initFailingRetriever) Status() retriever.Status                    { return retriever.RetrieverNotReady }
