package slacknotifier

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/notifier"
	"github.com/blackbirdworks/semaphore/testhelpers"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
	httpsHooksSlackCom = "https://hooks.slack.com/services/T00000000/B00000000/XXXXXXXXXXXXXXXXXXXXXXXX"
)

func TestSlackNotifier_Notify(t *testing.T) {
	t.Parallel()

	type args struct {
		diff       notifier.DiffCache
		url        string
		statusCode int
		forceError bool
	}

	type expected struct {
		errMsg    string
		bodyPath  string
		signature string
		err       bool
	}

	tests := []struct {
		name     string
		expected expected
		args     args
	}{
		{
			name: "should call webhook and have valid results",
			expected: expected{
				bodyPath: "./testdata/should_call_webhook_and_have_valid_results.json",
			},
			args: args{
				url:        httpsHooksSlackCom,
				statusCode: http.StatusOK,
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
						fixtures.FlagKeyTest: &flagdef.InternalFlag{
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
									fixtures.VariationDefault: new(any("strDefault")),
									fixtures.VariationFalse:   new(any("strFalse")),
									fixtures.VariationTrue:    new(any("strTrue")),
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
			},
		},
		{
			name: "should err if http code is superior to 399",
			expected: expected{
				err:    true,
				errMsg: "error: (Slack Notifier) while calling slack webhook, statusCode = 400",
			},
			args: args{
				url:        httpsHooksSlackCom,
				statusCode: http.StatusBadRequest,
				diff:       notifier.DiffCache{},
			},
		},
		{
			name: "should err if error while calling webhook",
			expected: expected{
				err:    true,
				errMsg: "error: (Slack Notifier) error: while calling webhook: random error",
			},
			args: args{
				url:        httpsHooksSlackCom,
				statusCode: http.StatusOK,
				diff:       notifier.DiffCache{},
				forceError: true,
			},
		},
		{
			name: "missing slack url",
			expected: expected{
				err: true,
				errMsg: "error: (Slack Notifier) invalid notifier configuration, no SlackWebhookURL provided for the slack no" +
					"tifier",
			},
			args: args{
				statusCode: http.StatusOK,
				diff:       notifier.DiffCache{},
			},
		},
		{
			name: "invalid slack url",
			expected: expected{
				err:    true,
				errMsg: `error: (Slack Notifier) invalid SlackWebhookURL: parse "[REDACTED]"`,
			},
			args: args{
				url:        "https://{}hooks.slack.com/services/T00000000/B00000000/XXXXXXXXXXXXXXXXXXXXXXXX",
				statusCode: http.StatusOK,
				diff:       notifier.DiffCache{},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockHTTPClient := &testhelpers.HTTPClientMock{
				StatusCode: tt.args.statusCode,
				ForceError: tt.args.forceError,
			}

			c := Notifier{
				SlackWebhookURL: tt.args.url,
				httpClient:      mockHTTPClient,
			}

			err := c.Notify(tt.args.diff)

			if tt.expected.err {
				assert.ErrorContains(t, err, tt.expected.errMsg)
			} else {
				require.NoError(t, err)

				hostname, _ := os.Hostname()
				content, _ := os.ReadFile(tt.expected.bodyPath)
				expectedContent := strings.ReplaceAll(string(content), "{{hostname}}", hostname)
				assert.JSONEq(t, expectedContent, mockHTTPClient.Body)
				assert.Equal(t, tt.expected.signature, mockHTTPClient.Signature)
			}
		})
	}
}

// otherFlag is a [flag.Flag] that DescribeChanges cannot describe.
type otherFlag struct{ flagdef.Flag }

func TestConvertUpdatedFlagsToSlackMessage_diffError(t *testing.T) {
	t.Parallel()

	broken := otherFlag{}
	attachments := convertUpdatedFlagsToSlackMessage(notifier.DiffCache{
		Updated: map[string]notifier.DiffUpdated{fixtures.FlagKeyTest: {Before: broken, After: broken}},
	})
	assert.Len(t, attachments, 1)
	assert.Contains(t, attachments[0].Text, notifier.ChangesUnavailable)
	assert.Contains(t, attachments[0].Text, "impossible to describe changes")
}

func TestConvertUpdatedFlagsToSlackMessage_escapesSlackControlCharacters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "should escape a channel mention", value: "<!channel>", want: "&lt;!channel&gt;"},
		{name: "should escape an ampersand", value: "a & b", want: "a &amp; b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			attachments := convertUpdatedFlagsToSlackMessage(notifier.DiffCache{
				Updated: map[string]notifier.DiffUpdated{fixtures.FlagKeyTest: {
					Before: &flagdef.InternalFlag{},
					After:  &flagdef.InternalFlag{Metadata: &map[string]any{"owner": tt.value}},
				}},
			})
			require.Len(t, attachments, 1)
			assert.Contains(t, attachments[0].Text, tt.want)
			assert.NotContains(t, attachments[0].Text, tt.value)
		})
	}
}
