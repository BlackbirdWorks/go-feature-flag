package ffclient_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/retriever/fileretriever"
)

const (
	keyAlice     = "alice"
	keyCarol     = "carol"
	flagBeta     = "beta-feature"
	flagBetaJSON = "beta-feature-json"
	flagDeny     = "deny-list"
	segBetaUsers = "beta-users"
	segChurned   = "churned"
)

var errSegmentStoreDown = errors.New("segment store down")

type downProvider struct{}

func (downProvider) Contains(context.Context, []segment.Lookup) error { return errSegmentStoreDown }

// segmentFlags is the flag file of the tests of this file. It is written to a temporary directory, not kept in
// testdata: it uses fields that only the engine knows.
const segmentFlags = `beta-feature:
  variations:
    enabled: true
    disabled: false
  targeting:
    - name: beta-not-churned
      query: targetingKey in segment("beta-users") and not (targetingKey in segment("churned"))
      variation: enabled
  defaultRule:
    variation: disabled

beta-feature-json:
  variations:
    enabled: true
    disabled: false
  targeting:
    - name: beta-json
      query: '{"inSegment": [{"var": "targetingKey"}, "beta-users"]}'
      variation: enabled
  defaultRule:
    variation: disabled

deny-list:
  variations:
    allowed: true
    denied: false
  targeting:
    - name: deny
      query: targetingKey in segment("blocked")
      onError: match
      variation: denied
  defaultRule:
    variation: allowed

plain-flag:
  variations:
    enabled: true
    disabled: false
  targeting:
    - name: pro
      query: plan eq "pro"
      variation: enabled
  defaultRule:
    variation: disabled

typed-user:
  variations:
    enabled: true
    disabled: false
  targeting:
    - name: typed
      query: user.id in segment("beta-users")
      variation: enabled
  defaultRule:
    variation: disabled

broken-flag:
  variations:
    enabled: true
    disabled: false
  targeting:
    - name: bad
      query: targetingKey in segment("Bad Name")
      variation: enabled
  defaultRule:
    variation: disabled
`

func newSegmentClient(t *testing.T, catalog *segment.Catalog, provider segment.Provider) *ffclient.GoFeatureFlag {
	t.Helper()

	path := filepath.Join(t.TempDir(), "flags.yaml")
	require.NoError(t, os.WriteFile(path, []byte(segmentFlags), 0o600))

	client, err := ffclient.New(&ffclient.Config{
		PollingInterval: time.Hour,
		Retriever:       &fileretriever.Retriever{Path: path},
		Segments:        catalog,
		SegmentProvider: provider,
	})
	require.NoError(t, err)
	t.Cleanup(client.Close)

	return client
}

func TestSegmentsThroughTheClient(t *testing.T) {
	t.Parallel()

	catalog, err := segment.NewCatalog(
		segment.Definition{Name: segBetaUsers, Type: segment.TypeFile},
		segment.Definition{Name: segChurned, Type: segment.TypeFile},
		segment.Definition{Name: "blocked", Type: segment.TypeFile},
	)
	require.NoError(t, err)

	healthy := newSegmentClient(t, catalog, segment.NewMemoryProvider(map[string][]string{
		segBetaUsers: {keyAlice, keyCarol},
		segChurned:   {keyCarol},
		"blocked":    {"mallory"},
	}))
	down := newSegmentClient(t, catalog, downProvider{})
	unconfigured := newSegmentClient(t, catalog, nil)

	tests := []struct {
		client     *ffclient.GoFeatureFlag
		name       string
		flag       string
		key        string
		plan       string
		wantValue  bool
		wantStatus segment.Status
	}{
		{
			name:       "member",
			client:     healthy,
			flag:       flagBeta,
			key:        keyAlice,
			wantValue:  true,
			wantStatus: segment.StatusHealthy,
		},
		{
			name:       "member but churned",
			client:     healthy,
			flag:       flagBeta,
			key:        keyCarol,
			wantValue:  false,
			wantStatus: segment.StatusHealthy,
		},
		{
			name:       "not member",
			client:     healthy,
			flag:       flagBeta,
			key:        "bob",
			wantValue:  false,
			wantStatus: segment.StatusHealthy,
		},
		{
			name:       "json member",
			client:     healthy,
			flag:       flagBetaJSON,
			key:        keyAlice,
			wantValue:  true,
			wantStatus: segment.StatusHealthy,
		},
		{
			name:       "json not member",
			client:     healthy,
			flag:       flagBetaJSON,
			key:        "bob",
			wantValue:  false,
			wantStatus: segment.StatusHealthy,
		},
		{
			name:       "deny list: blocked",
			client:     healthy,
			flag:       flagDeny,
			key:        "mallory",
			wantValue:  false,
			wantStatus: segment.StatusHealthy,
		},
		{
			name:       "deny list: allowed",
			client:     healthy,
			flag:       flagDeny,
			key:        keyAlice,
			wantValue:  true,
			wantStatus: segment.StatusHealthy,
		},
		{
			name:       "store down: rule skipped",
			client:     down,
			flag:       flagBeta,
			key:        keyAlice,
			wantValue:  false,
			wantStatus: segment.StatusStoreError,
		},
		{
			name:       "store down: deny rule matches",
			client:     down,
			flag:       flagDeny,
			key:        keyAlice,
			wantValue:  false,
			wantStatus: segment.StatusStoreError,
		},
		{
			name:       "no provider: rule skipped",
			client:     unconfigured,
			flag:       flagBetaJSON,
			key:        keyAlice,
			wantValue:  false,
			wantStatus: segment.StatusNotConfigured,
		},
		{
			name:       "flag without segments",
			client:     healthy,
			flag:       "plain-flag",
			key:        keyAlice,
			plan:       "pro",
			wantValue:  true,
			wantStatus: segment.StatusNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			evalCtx := ffcontext.NewEvaluationContextBuilder(tt.key).AddCustom("plan", tt.plan).Build()

			got, rawErr := tt.client.RawVariation(tt.flag, evalCtx, !tt.wantValue)
			require.NoError(t, rawErr)
			assert.Equal(t, tt.wantValue, got.Value)
			assert.Equal(t, tt.wantStatus, got.SegmentStatus)
			assert.Equal(t, tt.wantStatus == segment.StatusNone, got.Cacheable)
			assert.False(t, got.Failed)

			typed, typedErr := tt.client.BoolVariation(tt.flag, evalCtx, !tt.wantValue)
			require.NoError(t, typedErr)
			assert.Equal(t, tt.wantValue, typed)
		})
	}
}

func TestSegmentsThroughTheClientReason(t *testing.T) {
	t.Parallel()

	client := newSegmentClient(
		t,
		nil,
		segment.NewMemoryProvider(map[string][]string{segBetaUsers: {keyAlice}, segChurned: {}}),
	)

	member, err := client.BoolVariationDetails(flagBeta, ffcontext.NewEvaluationContext(keyAlice), false)
	require.NoError(t, err)
	assert.Equal(t, flagdef.ReasonTargetingMatch, member.Reason)
	assert.Equal(t, "enabled", member.VariationType)

	other, err := client.BoolVariationDetails(flagBeta, ffcontext.NewEvaluationContext("zed"), false)
	require.NoError(t, err)
	assert.Equal(t, flagdef.ReasonDefault, other.Reason)
	assert.Equal(t, "disabled", other.VariationType)
}

func TestSegmentsInvalidFlagIsNotLoaded(t *testing.T) {
	t.Parallel()

	client := newSegmentClient(t, nil, segment.NewMemoryProvider(nil))

	got, err := client.BoolVariation("broken-flag", ffcontext.NewEvaluationContext(keyAlice), true)
	require.Error(t, err, "a flag with an invalid segment name is rejected at load, like any invalid flag")
	assert.True(t, got, "the SDK default is returned")
}

func TestSegmentsInAllFlagsState(t *testing.T) {
	t.Parallel()

	client := newSegmentClient(
		t,
		nil,
		segment.NewMemoryProvider(map[string][]string{segBetaUsers: {keyAlice}, segChurned: {}}),
	)

	state := client.AllFlagsState(ffcontext.NewEvaluationContext(keyAlice))
	flags := state.GetFlags()

	require.Contains(t, flags, flagBeta)
	assert.Equal(t, true, flags[flagBeta].Value)
	require.Contains(t, flags, "plain-flag")
	assert.Equal(t, false, flags["plain-flag"].Value)
	assert.NotContains(t, flags, "broken-flag")

	otherState := client.AllFlagsState(ffcontext.NewEvaluationContext("bob"))
	other := otherState.GetFlags()
	assert.Equal(t, false, other[flagBeta].Value)
}

type clientUser struct {
	ID string `json:"id"`
}

func TestSegmentsReadTheAttributesOfTypedObjectsOfTheClient(t *testing.T) {
	t.Parallel()

	client := newSegmentClient(t, nil, segment.NewMemoryProvider(map[string][]string{"beta-users": {keyAlice}}))

	tests := []struct {
		user any
		name string
		want bool
	}{
		{name: "map of strings", user: map[string]string{"id": keyAlice}, want: true},
		{name: "struct with json tags", user: clientUser{ID: keyAlice}, want: true},
		{name: "map of any", user: map[string]any{"id": keyAlice}, want: true},
		{name: "another user", user: map[string]string{"id": "zed"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			evalCtx := ffcontext.NewEvaluationContextBuilder("k").AddCustom("user", tt.user).Build()

			got, err := client.BoolVariation("typed-user", evalCtx, !tt.want)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
