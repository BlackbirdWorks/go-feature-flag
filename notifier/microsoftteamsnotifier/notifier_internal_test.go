package microsoftteamsnotifier

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	goteamsnotify "github.com/atc0005/go-teams-notify/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/notifier"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

func TestMicrosoftTeamsNotifier_Notify(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		diff         notifier.DiffCache
		roundTripper *mockRoundTripper
		wantErr      assert.ErrorAssertionFunc
		want         string
		webhookURL   string
	}{
		{
			name: "should call webhook and have valid results",
			diff: notifier.DiffCache{
				Added: map[string]flagdef.Flag{
					"test-flag3": &flagdef.InternalFlag{
						Rules: &[]flagdef.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new("key eq \"random-key\""),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 95,
									fixtures.VariationTrue:  5,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any("default")),
							fixtures.VariationFalse:   new(any("false")),
							fixtures.VariationTrue:    new(any("test")),
						},
						DefaultRule: &flagdef.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
						TrackEvents: new(true),
						Disable:     new(false),
						Version:     new("1.1"),
						Metadata:    &map[string]any{notifier.UpdatedByMetadataKey: fixtures.UserJane},
					},
				},
				Deleted: map[string]flagdef.Flag{
					"test-flag": &flagdef.InternalFlag{
						Rules: &[]flagdef.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new("key eq \"random-key\""),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(false)),
							fixtures.VariationFalse:   new(any(false)),
							fixtures.VariationTrue:    new(any(true)),
						},
						Metadata: &map[string]any{notifier.UpdatedByMetadataKey: fixtures.UserJane},
						DefaultRule: &flagdef.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
					},
				},
				Updated: map[string]notifier.DiffUpdated{
					"test-flag2": {
						Before: &flagdef.InternalFlag{
							Variations: &map[string]*any{
								fixtures.VariationDefault: new(any(false)),
								fixtures.VariationFalse:   new(any(false)),
								fixtures.VariationTrue:    new(any(true)),
							},
							DefaultRule: &flagdef.Rule{
								Name: new(fixtures.RuleDefault),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
							Metadata: &map[string]any{notifier.UpdatedByMetadataKey: "john"},
							Experimentation: &flagdef.ExperimentationRollout{
								Start: new(time.Unix(1095379400, 0).UTC()),
								End:   new(time.Unix(1095371000, 0).UTC()),
							},
						},
						After: &flagdef.InternalFlag{
							Variations: &map[string]*any{
								fixtures.VariationDefault: new(any(true)),
								fixtures.VariationFalse:   new(any(false)),
								fixtures.VariationTrue:    new(any(true)),
							},
							Rules: &[]flagdef.Rule{
								{
									Name:  new(fixtures.RuleOne),
									Query: new("key eq \"not-a-ke\""),
									Percentages: &map[string]float64{
										fixtures.VariationFalse: 20,
										fixtures.VariationTrue:  80,
									},
								},
							},
							DefaultRule: &flagdef.Rule{
								Name:            new(fixtures.RuleDefault),
								VariationResult: new(fixtures.VariationDefault),
							},
							Disable:     new(true),
							TrackEvents: new(false),
							Version:     new("1.1"),
							Metadata:    &map[string]any{notifier.UpdatedByMetadataKey: fixtures.UserJane},
						},
					},
				},
			},
			roundTripper: newMockRoundTripper(http.StatusAccepted),
			wantErr:      assert.NoError,
			want:         "./testdata/should_call_webhook_and_have_valid_results.json",
			webhookURL: "https://prod-22.francecentral.logic.azure.com:443/workflows/XXXXXX/triggers/manual/paths/invoke" +
				"?api-version=2016-06-01&sp=%2Ftriggers%2Fmanual%2Frun&sv=1.0&sig=XXX",
		},
		{
			name:         "should err if http code is superior to 399",
			roundTripper: newMockRoundTripper(http.StatusBadRequest),
			wantErr:      assert.Error,
			webhookURL: "https://prod-22.francecentral.logic.azure.com:443/workflows/XXXXXX/triggers/manual/paths/invoke" +
				"?api-version=2016-06-01&sp=%2Ftriggers%2Fmanual%2Frun&sv=1.0&sig=XXX",
		},
		{
			name:         "missing microsoft teams url",
			diff:         notifier.DiffCache{},
			roundTripper: newMockRoundTripper(http.StatusAccepted),
			wantErr:      assert.Error,
		},
		{
			name:         "invalid microsoft teams url",
			diff:         notifier.DiffCache{},
			roundTripper: newMockRoundTripper(http.StatusAccepted),
			wantErr:      assert.Error,
			webhookURL: "https://{}}/workflows/XXXXXX/triggers/manual/paths/invoke?api-version=2016-06-01&sp=%2Ftriggers" +
				"%2Fmanual%2Frun&sv=1.0&sig=XXX",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mstClient := goteamsnotify.NewTeamsClient()
			if tt.roundTripper != nil {
				mstClient.SetHTTPClient(&http.Client{Transport: tt.roundTripper})
			}

			n := &Notifier{
				MicrosoftTeamsWebhookURL: tt.webhookURL,
				teamsClient:              mstClient,
			}

			err := n.Notify(tt.diff)
			tt.wantErr(t, err)

			if err != nil {
				return
			}

			hostname, _ := os.Hostname()
			wantBody, err := os.ReadFile(tt.want)
			require.NoError(t, err)
			assert.JSONEq(
				t,
				strings.ReplaceAll(string(wantBody), "{{hostname}}", hostname),
				tt.roundTripper.requestBody,
			)
		})
	}
}

func newMockRoundTripper(status int) *mockRoundTripper {
	return &mockRoundTripper{
		response: &http.Response{
			StatusCode: status,
			Body:       nil,
		},
	}
}

type mockRoundTripper struct {
	response    *http.Response
	requestBody string
}

func (rt *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}

	rt.requestBody = string(body)
	req.Body = io.NopCloser(bytes.NewBuffer(body))

	return rt.response, nil
}

// otherFlag is a [flag.Flag] that DescribeChanges cannot describe.
type otherFlag struct{ flagdef.Flag }

func TestConvertToMicrosoftTeamsMessage_diffError(t *testing.T) {
	t.Parallel()

	broken := otherFlag{}
	msg := convertToMicrosoftTeamsMessage(notifier.DiffCache{
		Updated: map[string]notifier.DiffUpdated{"test-flag": {Before: broken, After: broken}},
	})
	assert.Contains(t, msg, notifier.ChangesUnavailable+": impossible to describe changes")
}
