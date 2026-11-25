package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	awsConf "github.com/aws/aws-sdk-go-v2/config"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	cmdlog "github.com/blackbirdworks/semaphore/cmdhelpers/log"
	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
	"github.com/blackbirdworks/semaphore/retriever"
	"github.com/blackbirdworks/semaphore/retriever/s3retrieverv2"
)

var (
	errEnableSharedPollerPerBucketRequiresASingle = errors.New(
		"enableSharedPollerPerBucket requires a single s3 retriever with item set",
	)
)

// sharedItemsFactory builds the shared group of a bucket.
type sharedItemsFactory func(ctx context.Context, bucket string) (*s3retrieverv2.SharedItems, error)

func defaultSharedItemsFactory(ctx context.Context, bucket string) (*s3retrieverv2.SharedItems, error) {
	awsConfig, err := awsConf.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load AWS default config: %w", err)
	}

	items, err := s3retrieverv2.NewSharedItems(ctx, &s3retrieverv2.SharedItemsOptions{
		Bucket:          bucket,
		AwsConfig:       &awsConfig,
		Lister:          nil,
		Downloader:      nil,
		S3ClientOptions: nil,
	})
	if err != nil {
		return nil, fmt.Errorf("init shared S3 items: %w", err)
	}

	return items, nil
}

// sharedBucketRegistry lazily creates one shared S3 group per bucket and hands out the
// retrievers of the flagsets using enableSharedPollerPerBucket. A group is dropped once all its
// retrievers have been shut down.
type sharedBucketRegistry struct {
	factory sharedItemsFactory
	logger  *slog.Logger
	groups  map[string]*registryGroup
	// pending are the retrievers created by preRegister and not claimed yet.
	pending map[string][]*releasingRetriever
	// polling is the polling configuration of the relay proxy (top-level pollingInterval and
	// enablePollingJitter), shared by all the groups.
	polling s3retrieverv2.PollerConfig
	mu      sync.Mutex
}

type registryGroup struct {
	items *s3retrieverv2.SharedItems
	refs  int
}

// newSharedBucketRegistry builds the registry. The groups poll their bucket every pollingInterval
// (the top-level pollingInterval of the relay proxy, 0 means 60s) with the jitter if enabled.
func newSharedBucketRegistry(
	factory sharedItemsFactory, pollingInterval time.Duration, jitter bool, logger *slog.Logger,
) *sharedBucketRegistry {
	if factory == nil {
		factory = defaultSharedItemsFactory
	}

	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	return &sharedBucketRegistry{
		factory: factory,
		logger:  logger,
		polling: s3retrieverv2.PollerConfig{
			Interval: normalizePollingInterval(pollingInterval),
			Jitter:   jitter,
			Logger:   nil,
		},
		groups:  map[string]*registryGroup{},
		pending: map[string][]*releasingRetriever{},
	}
}

// pendingKey identifies the retrievers created by preRegister for an item of a bucket.
func pendingKey(fs *config.FlagSet) string {
	rc := fs.Retriever

	return fmt.Sprintf("%s\x00%s", rc.Bucket, rc.Item)
}

// retrieverFor returns the retriever of the flagset (its configuration is expected to be valid).
// The reference on the bucket group is released when the retriever is shut down.
func (r *sharedBucketRegistry) retrieverFor(flagset *config.FlagSet) (*releasingRetriever, error) {
	err := checkSharedPollerFlagSet(flagset)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()

	key := pendingKey(flagset)
	if p := r.pending[key]; len(p) > 0 {
		r.pending[key] = p[1:]
		r.mu.Unlock()

		return p[0], nil
	}
	r.mu.Unlock()

	return r.newRetriever(flagset)
}

// preRegister registers the item of the flagset in its bucket group so that the first listing
// already covers it. The next retrieverFor with the same configuration returns this retriever,
// the ones that are not claimed must be released with releasePending.
func (r *sharedBucketRegistry) preRegister(flagset *config.FlagSet) error {
	err := checkSharedPollerFlagSet(flagset)
	if err != nil {
		return err
	}

	relRetriever, err := r.newRetriever(flagset)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	key := pendingKey(flagset)
	r.pending[key] = append(r.pending[key], relRetriever)

	return nil
}

// preRegisterAll pre-registers the flagsets using enableSharedPollerPerBucket, an invalid one is only
// logged (its client creation reports the error).
func (r *sharedBucketRegistry) preRegisterAll(flagsets []config.FlagSet) {
	for i := range flagsets {
		if !flagsets[i].EnableSharedPollerPerBucket {
			continue
		}

		err := r.preRegister(&flagsets[i])
		if err != nil {
			r.logger.LogAttrs(
				context.Background(),
				slog.LevelWarn,
				"unable to pre-register the shared-poller retriever of the flagset",
				slog.String("flagset", flagsets[i].Name),
				cmdlog.Err(err),
			)
		}
	}
}

// releasePending shuts down the pre-registered retrievers nobody claimed.
func (r *sharedBucketRegistry) releasePending() {
	r.mu.Lock()

	var unclaimed []*releasingRetriever
	for k, p := range r.pending {
		unclaimed = append(unclaimed, p...)

		delete(r.pending, k)
	}
	r.mu.Unlock()

	for _, rr := range unclaimed {
		_ = rr.Shutdown(context.Background())
	}
}

func checkSharedPollerFlagSet(fs *config.FlagSet) error {
	rc := fs.Retriever
	if fs.Retrievers != nil || rc == nil || rc.Kind != retrieverconf.S3Retriever || rc.Item == "" {
		return errEnableSharedPollerPerBucketRequiresASingle
	}

	return nil
}

// acquire returns the group of the bucket, with one more reference. The group is built outside of
// the lock (it loads the AWS configuration), if another caller built it meanwhile theirs is used.
func (r *sharedBucketRegistry) acquire(bucket string) (*registryGroup, error) {
	r.mu.Lock()
	if grp, ok := r.groups[bucket]; ok {
		grp.refs++
		r.mu.Unlock()

		return grp, nil
	}
	r.mu.Unlock()

	items, err := r.factory(context.Background(), bucket)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	grp, ok := r.groups[bucket]
	if !ok {
		grp = &registryGroup{items: items}
		items.SetPoller(r.pollerConfig(bucket))
		r.groups[bucket] = grp
	}

	grp.refs++

	return grp, nil
}

// newRetriever creates the retriever of the flagset in the group of its bucket.
func (r *sharedBucketRegistry) newRetriever(flagset *config.FlagSet) (*releasingRetriever, error) {
	retConf := flagset.Retriever

	grp, err := r.acquire(retConf.Bucket)
	if err != nil {
		return nil, err
	}

	r.warnPollingMismatch(flagset)

	return &releasingRetriever{
		SharedItemRetriever: grp.items.Retriever(retConf.Item),
		group:               grp.items,
		release:             func() { r.release(retConf.Bucket, grp) },
	}, nil
}

// normalizePollingInterval applies the default (60s) of the unset pollingInterval.
func normalizePollingInterval(interval time.Duration) time.Duration {
	if interval <= 0 {
		return s3retrieverv2.DefaultSharedPollingInterval
	}

	return interval
}

// pollerConfig is the polling configuration of the group of a bucket. The caller must hold r.mu.
func (r *sharedBucketRegistry) pollerConfig(bucket string) s3retrieverv2.PollerConfig {
	cfg := r.polling
	cfg.Logger = r.logger.
		With(slog.String("component", "shared-poller-poller"), slog.String("bucket", bucket))

	return cfg
}

// warnPollingMismatch warns when a flagset sets a pollingInterval or an enablePollingJitter that
// differs from the top-level ones, which are the ones used by the shared poller.
func (r *sharedBucketRegistry) warnPollingMismatch(flagset *config.FlagSet) {
	r.mu.Lock()
	parent := r.polling
	r.mu.Unlock()

	own := time.Duration(flagset.PollingInterval) * time.Millisecond
	if own != 0 && own != parent.Interval {
		r.warnIgnored(flagset, "pollingInterval",
			slog.Duration("flagsetPollingInterval", own), slog.Duration("pollingInterval", parent.Interval))
	}

	if flagset.EnablePollingJitter && !parent.Jitter {
		r.warnIgnored(flagset, "enablePollingJitter",
			slog.Bool("flagsetEnablePollingJitter", flagset.EnablePollingJitter),
			slog.Bool("enablePollingJitter", parent.Jitter))
	}
}

// warnIgnored warns that a polling setting of the flagset is ignored in favour of the top-level one.
func (r *sharedBucketRegistry) warnIgnored(flagset *config.FlagSet, setting string, fields ...slog.Attr) {
	r.logger.LogAttrs(
		context.Background(),
		slog.LevelWarn,
		"enableSharedPollerPerBucket: a polling setting of the flagset is ignored, the top-level one is used",
		append([]slog.Attr{slog.String("flagset", flagset.Name), slog.String("setting", setting)}, fields...)...)
}

// setPolling applies a new top-level pollingInterval and enablePollingJitter to the running
// groups, and warns about the given flagsets that set different ones (the caller leaves out the
// flagsets that are about to be added: their retriever warns). Nothing happens if they did not
// change.
func (r *sharedBucketRegistry) setPolling(
	interval time.Duration, jitter bool, flagsets []config.FlagSet,
) {
	interval = normalizePollingInterval(interval)

	r.mu.Lock()
	if r.polling.Interval == interval && r.polling.Jitter == jitter {
		r.mu.Unlock()

		return
	}

	r.polling.Interval, r.polling.Jitter = interval, jitter
	for bucket, g := range r.groups {
		g.items.SetPoller(r.pollerConfig(bucket))
	}
	r.mu.Unlock()
	r.logger.LogAttrs(
		context.Background(),
		slog.LevelInfo,
		"Configuration changed: polling of the shared pollers updated",
		slog.Duration("pollingInterval", interval),
		slog.Bool("enablePollingJitter", jitter),
	)

	for i := range flagsets {
		if flagsets[i].EnableSharedPollerPerBucket {
			r.warnPollingMismatch(&flagsets[i])
		}
	}
}

func (r *sharedBucketRegistry) release(bucket string, g *registryGroup) {
	r.mu.Lock()
	defer r.mu.Unlock()

	g.refs--
	if g.refs <= 0 && r.groups[bucket] == g {
		delete(r.groups, bucket)
	}
}

// releasingRetriever releases its reference on the group the first time it is shut down.
type releasingRetriever struct {
	*s3retrieverv2.SharedItemRetriever

	release     func()
	group       *s3retrieverv2.SharedItems
	releaseOnce sync.Once
}

var _ retriever.InitializableRetriever = (*releasingRetriever)(nil)

// SetRefresh registers the function the poller of the bucket group calls when the object of the
// retriever changed, which starts the poller if it is not running.
func (r *releasingRetriever) SetRefresh(refresh func() bool) {
	r.group.SetRefresh(r.SharedItemRetriever, refresh)
}

func (r *releasingRetriever) Shutdown(ctx context.Context) error {
	err := r.SharedItemRetriever.Shutdown(ctx)
	r.releaseOnce.Do(r.release)

	if err != nil {
		return fmt.Errorf("shutdown shared S3 retriever: %w", err)
	}

	return nil
}
