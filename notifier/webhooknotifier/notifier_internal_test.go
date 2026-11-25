package webhooknotifier

import (
	"encoding/json"
	"net/http"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/notifier"
	"github.com/blackbirdworks/semaphore/testutils"
)

const (
	defaultRule            = "defaultRule"
	fixtureDefault         = "Default"
	fixtureDefault2        = "default"
	fixtureFalse           = "False"
	fixtureFalse2          = "false"
	fixtureTrue            = "True"
	httpWebhookExampleHook = "http://webhook.example/hook"
	rule1                  = "rule1"
	test                   = "test"
	testFlag3              = "test-flag3"
)

type webhookNotifierFields struct {
	Secret string
}

type webhookNotifierExpected struct {
	headers   map[string][]string
	errorMsg  string
	bodyPath  string
	signature string
	err       bool
}

type webhookNotifierArgs struct {
	diff       notifier.DiffCache
	headers    map[string][]string
	url        string
	statusCode int
	forceError bool
}

type webhookNotifierCase struct {
	name     string
	fields   webhookNotifierFields
	expected webhookNotifierExpected
	args     webhookNotifierArgs
}

func runWebhookNotifierTests(t *testing.T, tests []webhookNotifierCase) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockHTTPClient := &testutils.HTTPClientMock{
				StatusCode: tt.args.statusCode,
				ForceError: tt.args.forceError,
			}

			whNotifier := Notifier{
				EndpointURL: tt.args.url,
				Secret:      tt.fields.Secret,
				Meta:        map[string]string{hostname2: "toto"},
				httpClient:  mockHTTPClient,
				init:        sync.Once{},
				Headers:     tt.args.headers,
			}

			err := whNotifier.Notify(tt.args.diff)

			if tt.expected.err {
				assert.ErrorContains(t, err, tt.expected.errorMsg)
			} else {
				require.NoError(t, err)

				content, _ := os.ReadFile(tt.expected.bodyPath)
				assert.JSONEq(t, string(content), mockHTTPClient.Body)
				assert.Equal(t, tt.expected.signature, mockHTTPClient.Signature)

				if tt.expected.headers != nil {
					assert.Equal(t, tt.expected.headers, mockHTTPClient.Headers)
				}
			}
		})
	}
}

func Test_webhookNotifier_Notify(t *testing.T) {
	t.Parallel()

	runWebhookNotifierTests(t, []webhookNotifierCase{
		{
			name: "should call webhook and have valid results",
			fields: webhookNotifierFields{
				Secret: testSecret,
			},
			expected: webhookNotifierExpected{
				bodyPath:  "./testdata/should_call_webhook_and_have_valid_results.json",
				signature: "sha256=813bb118d9ac870a1264c2e5ce2a9a95c46246c52312ff77201d6d6b826f4ed6",
			},
			args: webhookNotifierArgs{
				url:        httpWebhookExampleHook,
				statusCode: http.StatusOK,
				diff: notifier.DiffCache{
					Added: map[string]flag.Flag{
						testFlag3: &flag.InternalFlag{
							Variations: &map[string]*any{
								fixtureDefault: new(any(fixtureDefault2)),
								fixtureFalse:   new(any(fixtureFalse2)),
								fixtureTrue:    new(any(test)),
							},
							DefaultRule: &flag.Rule{
								Name: new(defaultRule),
								Percentages: &map[string]float64{
									fixtureFalse: 95,
									fixtureTrue:  5,
								},
							},
						},
					},
					Deleted: map[string]flag.Flag{
						"test-flag": &flag.InternalFlag{
							Rules: &[]flag.Rule{
								{
									Name:  new(rule1),
									Query: new("key eq \"random-key\""),
									Percentages: &map[string]float64{
										fixtureFalse: 0,
										fixtureTrue:  100,
									},
								},
							},
							Variations: &map[string]*any{
								fixtureDefault: new(any(false)),
								fixtureFalse:   new(any(false)),
								fixtureTrue:    new(any(true)),
							},
							DefaultRule: &flag.Rule{
								Name:            new(defaultRule),
								VariationResult: new(fixtureDefault),
							},
						},
					},
					Updated: map[string]notifier.DiffUpdated{
						"test-flag2": {
							Before: &flag.InternalFlag{
								Rules: &[]flag.Rule{
									{
										Name:  new(rule1),
										Query: new("key eq \"not-a-key\""),
										Percentages: &map[string]float64{
											fixtureFalse: 0,
											fixtureTrue:  100,
										},
									},
								},
								Variations: &map[string]*any{
									fixtureDefault: new(any(false)),
									fixtureFalse:   new(any(false)),
									fixtureTrue:    new(any(true)),
								},
								DefaultRule: &flag.Rule{
									Name:            new(defaultRule),
									VariationResult: new(fixtureDefault),
								},
							},
							After: &flag.InternalFlag{
								Rules: &[]flag.Rule{
									{
										Name:  new(rule1),
										Query: new("key eq \"not-a-key\""),
										Percentages: &map[string]float64{
											fixtureFalse: 0,
											fixtureTrue:  100,
										},
									},
								},
								Variations: &map[string]*any{
									fixtureDefault: new(any(false)),
									fixtureFalse:   new(any(false)),
									fixtureTrue:    new(any(true)),
								},
								DefaultRule: &flag.Rule{
									Name:            new(defaultRule),
									VariationResult: new(fixtureDefault),
								},
								Disable: new(true),
							},
						},
					},
				},
			},
		},
		{
			name: "should not be signed if no secret",
			expected: webhookNotifierExpected{
				bodyPath:  "./testdata/should_not_be_signed_if_no_secret.json",
				signature: "",
			},
			args: webhookNotifierArgs{
				url:        httpWebhookExampleHook,
				statusCode: http.StatusOK,
				diff: notifier.DiffCache{
					Added: map[string]flag.Flag{
						testFlag3: &flag.InternalFlag{
							Variations: &map[string]*any{
								fixtureDefault: new(any(fixtureDefault2)),
								fixtureFalse:   new(any(fixtureFalse2)),
								fixtureTrue:    new(any(test)),
							},
							DefaultRule: &flag.Rule{
								Name: new(defaultRule),
								Percentages: &map[string]float64{
									fixtureFalse: 95,
									fixtureTrue:  5,
								},
							},
						},
					},
					Deleted: map[string]flag.Flag{},
					Updated: map[string]notifier.DiffUpdated{},
				},
			},
		},
	})
}

func Test_webhookNotifier_Notify_ErrorsAndHeaders(t *testing.T) {
	t.Parallel()

	runWebhookNotifierTests(t, []webhookNotifierCase{
		{
			name: "should log if http code is superior to 399",
			expected: webhookNotifierExpected{
				err:      true,
				errorMsg: "error: while calling webhook, statusCode = 400",
			},
			args: webhookNotifierArgs{
				url:        httpWebhookExampleHook,
				statusCode: http.StatusBadRequest,
				diff:       notifier.DiffCache{},
			},
		},
		{
			name: "should log if error while calling webhook",
			expected: webhookNotifierExpected{
				err:      true,
				errorMsg: "error: while calling webhook: random error",
			},
			args: webhookNotifierArgs{
				url:        httpWebhookExampleHook,
				statusCode: http.StatusOK,
				diff:       notifier.DiffCache{},
				forceError: true,
			},
		},
		{
			name: "no endpointURL",
			expected: webhookNotifierExpected{
				err:      true,
				errorMsg: "invalid notifier configuration, no endpointURL provided for the webhook notifier",
			},
			args: webhookNotifierArgs{
				url:        "",
				statusCode: http.StatusOK,
				diff:       notifier.DiffCache{},
				forceError: true,
			},
		},
		{
			name: "should use custom Headers",
			expected: webhookNotifierExpected{
				bodyPath:  "./testdata/should_not_be_signed_if_no_secret.json",
				signature: "",
				headers: map[string][]string{
					authorization: {"Bearer auth_token"},
					contentType:   {applicationJson},
				},
			},
			args: webhookNotifierArgs{
				url:        httpWebhookExampleHook,
				statusCode: http.StatusOK,
				headers: map[string][]string{
					authorization: {"Bearer auth_token"},
				},
				diff: notifier.DiffCache{
					Added: map[string]flag.Flag{
						testFlag3: &flag.InternalFlag{
							Variations: &map[string]*any{
								fixtureDefault: new(any(fixtureDefault2)),
								fixtureFalse:   new(any(fixtureFalse2)),
								fixtureTrue:    new(any(test)),
							},
							DefaultRule: &flag.Rule{
								Name: new(defaultRule),
								Percentages: &map[string]float64{
									fixtureFalse: 95,
									fixtureTrue:  5,
								},
							},
						},
					},
					Deleted: map[string]flag.Flag{},
					Updated: map[string]notifier.DiffUpdated{},
				},
			},
		},
	})
}

func Test_webhookNotifier_no_meta_data(t *testing.T) {
	t.Parallel()

	mockHTTPClient := &testutils.HTTPClientMock{StatusCode: 200, ForceError: false}
	diff := notifier.DiffCache{
		Added: map[string]flag.Flag{
			testFlag3: &flag.InternalFlag{
				Variations: &map[string]*any{
					fixtureDefault: new(any(fixtureDefault2)),
					fixtureFalse:   new(any(fixtureFalse2)),
					fixtureTrue:    new(any(test)),
				},
				DefaultRule: &flag.Rule{
					Name: new(defaultRule),
					Percentages: &map[string]float64{
						fixtureFalse: 95,
						fixtureTrue:  5,
					},
				},
			},
		},
		Deleted: map[string]flag.Flag{},
		Updated: map[string]notifier.DiffUpdated{},
	}

	// no meta
	whNotifier := Notifier{
		EndpointURL: httpWebhookExampleHook,
		httpClient:  mockHTTPClient,
		init:        sync.Once{},
	}

	err := whNotifier.Notify(diff)

	require.NoError(t, err)

	var m map[string]any

	_ = json.Unmarshal([]byte(mockHTTPClient.Body), &m)

	assert.NotEmpty(t, m["meta"])
}
