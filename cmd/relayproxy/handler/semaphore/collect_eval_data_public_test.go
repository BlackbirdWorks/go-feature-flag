package semaphore_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	controller "github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/internal/protocol"
)

// publicFlagSet is a flag set that is the public one, or not, and that holds some flags.
type publicFlagSet struct {
	service.FlagSet

	flags    map[string]struct{}
	accepted int
	dropped  int
	public   bool
}

func (p *publicFlagSet) IsPublic() bool { return p.public }

func (p *publicFlagSet) HasFlag(key string) bool {
	_, ok := p.flags[key]

	return ok
}

func (p *publicFlagSet) DropNotPublic(count int) { p.dropped += count }

func (p *publicFlagSet) AcceptEventData(*exporter.FeatureEvent) bool {
	p.accepted++

	return true
}

func (p *publicFlagSet) CollectEventData(*exporter.FeatureEvent) { p.accepted++ }

type publicResolver struct {
	service.FlagSetResolver

	set *publicFlagSet
}

//nolint:ireturn // the seam of the HTTP layer
func (r publicResolver) FlagSet(string) (service.FlagSet, error) { return r.set, nil }

// The events of a caller of the public flag set are accepted for its flags only: the others are dropped and counted,
// and the answer counts only what was ingested. A flag set that is not the public one refuses nothing.
func TestCollectEvalData_PublicScope(t *testing.T) {
	t.Parallel()

	event := func(key string) string {
		return `{"kind":"feature","contextKind":"user","userKey":"u","creationDate":1,"key":"` + key +
			`","variation":"v","value":true,"default":false}`
	}

	body := `{"meta":{"app":"x"},"events":[` + event("pub") + "," + event("private") + "," + event("nope") + `]}`

	tests := []struct {
		name         string
		public       bool
		wantIngested int
		wantAccepted int
		wantDropped  int
	}{
		{name: "the public flag set", public: true, wantIngested: 1, wantAccepted: 1, wantDropped: 2},
		{name: "a flag set with a key", public: false, wantIngested: 3, wantAccepted: 3, wantDropped: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			set := &publicFlagSet{flags: map[string]struct{}{"pub": {}}, public: tt.public}
			ctrl := controller.NewCollectEvalData(publicResolver{set: set}, &metric.Metrics{})

			out, err := ctrl.Handle(context.Background(), &controller.CollectEvalDataInput{
				Request: controller.NewRequest("", protocol.MIMEApplicationJSON), RawBody: []byte(body),
			})
			require.NoError(t, err)

			assert.Equal(t, tt.wantIngested, out.Body.IngestedContentCount)
			assert.Equal(t, tt.wantAccepted, set.accepted)
			assert.Equal(t, tt.wantDropped, set.dropped)
		})
	}
}
