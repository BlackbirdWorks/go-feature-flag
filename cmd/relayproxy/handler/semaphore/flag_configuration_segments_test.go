package semaphore_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	controller "github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/testdata/mock"
	"github.com/blackbirdworks/semaphore/internal/protocol"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

var errNoBody = errors.New("the stub flag has no body")

// stubFlag is a flag that encodes as the JSON it is given.
type stubFlag struct {
	flagdef.Flag

	body string
}

func (f stubFlag) MarshalJSON() ([]byte, error) {
	if f.body == "" {
		return nil, errNoBody
	}

	return []byte(f.body), nil
}

const (
	variationsBody = `{"variations":{"on":true}}`
	flagDyn        = "dyn"
	flagStatic     = "static"
	flagPlain      = "plain"
	flagEmpty      = "empty"
)

// stubSet is a flag set of stub flags. It lists segments for the flags of use, as a flag set that implements
// [service.SegmentUser] does.
type stubSet struct {
	service.FlagSet

	flags map[string]flagdef.Flag
	use   map[string]segmentUse
}

type segmentUse struct {
	names    []string
	requires bool
}

func (s *stubSet) GetFlagsFromCache() (map[string]flagdef.Flag, error) { return s.flags, nil }

func (*stubSet) GetCacheRefreshDate() time.Time { return time.Time{} }

func (*stubSet) GetEvaluationContextEnrichment() map[string]any { return nil }

// segmentSet adds the SegmentUser interface to a stub set.
type segmentSet struct {
	*stubSet
}

func (s segmentSet) FlagSegments(flagKey string) ([]string, bool) {
	use := s.use[flagKey]

	return use.names, use.requires
}

// The flag configuration lists, for a flag whose rules use segments, the names of the segments and whether the flag
// needs the server to be evaluated (a dynamic segment); a flag that uses none has neither field. The names of the
// segments are all that is listed.
func TestFlagConfigurationAPICtrl_Handle_Segments(t *testing.T) {
	t.Parallel()

	flags := func() map[string]flagdef.Flag {
		return map[string]flagdef.Flag{
			flagDyn:    stubFlag{body: variationsBody},
			flagStatic: stubFlag{body: variationsBody},
			flagPlain:  stubFlag{body: variationsBody},
			flagEmpty:  stubFlag{body: `{}`},
		}
	}

	uses := map[string]segmentUse{
		flagDyn:    {names: []string{"audience", betaFlag}, requires: true},
		flagStatic: {names: []string{betaFlag}},
		flagEmpty:  {names: []string{betaFlag}},
	}

	tests := []struct {
		set       service.FlagSet
		wantFlags map[string]string
		name      string
	}{
		{
			name: "flags that use segments",
			set:  segmentSet{&stubSet{flags: flags(), use: uses}},
			wantFlags: map[string]string{
				flagDyn:    `{"variations":{"on":true},"segments":["audience","beta"],"requiresServerEvaluation":true}`,
				flagStatic: `{"variations":{"on":true},"segments":["beta"],"requiresServerEvaluation":false}`,
				flagPlain:  variationsBody,
				flagEmpty:  `{"segments":["beta"],"requiresServerEvaluation":false}`,
			},
		},
		{
			name: "a flag set that does not list segments",
			set:  &stubSet{flags: flags(), use: uses},
			wantFlags: map[string]string{
				flagDyn:    variationsBody,
				flagStatic: variationsBody,
				flagPlain:  variationsBody,
				flagEmpty:  `{}`,
			},
		},
		{
			name: "no flag uses a segment",
			set:  segmentSet{&stubSet{flags: flags()}},
			wantFlags: map[string]string{
				flagDyn:    variationsBody,
				flagStatic: variationsBody,
				flagPlain:  variationsBody,
				flagEmpty:  `{}`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctrl := controller.NewAPIFlagConfiguration(&mock.Resolver{Set: tt.set}, &metric.Metrics{})

			out, err := ctrl.Handle(t.Context(), &controller.FlagConfigurationInput{
				Request: controller.NewRequest("", protocol.MIMEApplicationJSON),
				RawBody: []byte(`{}`),
			})
			require.NoError(t, err)
			require.Equal(t, 200, out.Status)

			encoded, err := json.Marshal(out.Body.Flags)
			require.NoError(t, err)

			var got map[string]json.RawMessage

			require.NoError(t, json.Unmarshal(encoded, &got))
			require.Len(t, got, len(tt.wantFlags))

			for key, want := range tt.wantFlags {
				assert.JSONEq(t, want, string(got[key]), key)
			}
		})
	}
}

// A flag that is not encoded as a JSON object cannot carry the fields: the response fails to encode instead of
// producing a document that is not valid.
func TestFlagConfigurationAPICtrl_Handle_SegmentsOfAFlagThatIsNotAnObject(t *testing.T) {
	t.Parallel()

	set := segmentSet{&stubSet{
		flags: map[string]flagdef.Flag{"odd": stubFlag{body: `[1]`}},
		use:   map[string]segmentUse{"odd": {names: []string{betaFlag}}},
	}}

	ctrl := controller.NewAPIFlagConfiguration(&mock.Resolver{Set: set}, &metric.Metrics{})

	out, err := ctrl.Handle(t.Context(), &controller.FlagConfigurationInput{
		Request: controller.NewRequest("", protocol.MIMEApplicationJSON),
		RawBody: []byte(`{}`),
	})
	require.NoError(t, err)

	_, marshalErr := json.Marshal(out.Body.Flags)
	require.Error(t, marshalErr)
	assert.Contains(t, marshalErr.Error(), "the flag is not encoded as a JSON object")
}
