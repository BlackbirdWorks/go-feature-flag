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

	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/notifier"
)

func TestMicrosoftTeamsNotifier_Notify(t *testing.T) {
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
				Added: map[string]flag.Flag{
					"test-flag3": &flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new("rule1"),
								Query: new("key eq \"random-key\""),
								Percentages: &map[string]float64{
									"False": 95,
									"True":  5,
								},
							},
						},
						Variations: &map[string]*any{
							"Default": new(any("default")),
							"False":   new(any("false")),
							"True":    new(any("test")),
						},
						DefaultRule: &flag.Rule{
							Name:            new("defaultRule"),
							VariationResult: new("Default"),
						},
						TrackEvents: new(true),
						Disable:     new(false),
						Version:     new("1.1"),
						Metadata:    &map[string]any{notifier.UpdatedByMetadataKey: "jane"},
					},
				},
				Deleted: map[string]flag.Flag{
					"test-flag": &flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new("rule1"),
								Query: new("key eq \"random-key\""),
								Percentages: &map[string]float64{
									"False": 0,
									"True":  100,
								},
							},
						},
						Variations: &map[string]*any{
							"Default": new(any(false)),
							"False":   new(any(false)),
							"True":    new(any(true)),
						},
						Metadata: &map[string]any{notifier.UpdatedByMetadataKey: "jane"},
						DefaultRule: &flag.Rule{
							Name:            new("defaultRule"),
							VariationResult: new("Default"),
						},
					},
				},
				Updated: map[string]notifier.DiffUpdated{
					"test-flag2": {
						Before: &flag.InternalFlag{
							Variations: &map[string]*any{
								"Default": new(any(false)),
								"False":   new(any(false)),
								"True":    new(any(true)),
							},
							DefaultRule: &flag.Rule{
								Name: new("defaultRule"),
								Percentages: &map[string]float64{
									"False": 0,
									"True":  100,
								},
							},
							Metadata: &map[string]any{notifier.UpdatedByMetadataKey: "john"},
							Experimentation: &flag.ExperimentationRollout{
								Start: new(time.Unix(1095379400, 0).UTC()),
								End:   new(time.Unix(1095371000, 0).UTC()),
							},
						},
						After: &flag.InternalFlag{
							Variations: &map[string]*any{
								"Default": new(any(true)),
								"False":   new(any(false)),
								"True":    new(any(true)),
							},
							Rules: &[]flag.Rule{
								{
									Name:  new("rule1"),
									Query: new("key eq \"not-a-ke\""),
									Percentages: &map[string]float64{
										"False": 20,
										"True":  80,
									},
								},
							},
							DefaultRule: &flag.Rule{
								Name:            new("defaultRule"),
								VariationResult: new("Default"),
							},
							Disable:     new(true),
							TrackEvents: new(false),
							Version:     new("1.1"),
							Metadata:    &map[string]any{notifier.UpdatedByMetadataKey: "jane"},
						},
					},
				},
			},
			roundTripper: newMockRoundTripper(http.StatusAccepted),
			wantErr:      assert.NoError,
			want:         "./testdata/should_call_webhook_and_have_valid_results.json",
			webhookURL:   "https://prod-22.francecentral.logic.azure.com:443/workflows/XXXXXX/triggers/manual/paths/invoke?api-version=2016-06-01&sp=%2Ftriggers%2Fmanual%2Frun&sv=1.0&sig=XXX",
		},
		{
			name:         "should err if http code is superior to 399",
			roundTripper: newMockRoundTripper(http.StatusBadRequest),
			wantErr:      assert.Error,
			webhookURL:   "https://prod-22.francecentral.logic.azure.com:443/workflows/XXXXXX/triggers/manual/paths/invoke?api-version=2016-06-01&sp=%2Ftriggers%2Fmanual%2Frun&sv=1.0&sig=XXX",
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
			webhookURL:   "https://{}}/workflows/XXXXXX/triggers/manual/paths/invoke?api-version=2016-06-01&sp=%2Ftriggers%2Fmanual%2Frun&sv=1.0&sig=XXX",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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

// otherFlag is a flag.Flag that DescribeChanges cannot describe.
type otherFlag struct{ flag.Flag }

func TestConvertToMicrosoftTeamsMessage_diffError(t *testing.T) {
	broken := otherFlag{}
	msg := convertToMicrosoftTeamsMessage(notifier.DiffCache{
		Updated: map[string]notifier.DiffUpdated{"test-flag": {Before: broken, After: broken}},
	})
	assert.Contains(t, msg, notifier.ChangesUnavailable+": impossible to describe changes")
}
