package controller_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	controller "github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
	"github.com/blackbirdworks/semaphore/notifier"
)

func TestPIFlagChange_WithConfigChange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		apiKey        string
		wantFile      string
		wantAfterFile string
		config        config.Config
	}{
		{
			name: "default mode",
			config: config.Config{
				CommonFlagSet: config.CommonFlagSet{
					PollingInterval: 1000,
					Retrievers: &[]retrieverconf.RetrieverConf{
						{
							Kind: "file",
							Path: testdataDir + "/config_flags.yaml",
						},
					},
				},
			},
			wantFile:      "flag_change_with_config_change.json",
			wantAfterFile: "flag_change_after_config_change.json",
		},
		{
			name: "flagset mode",
			config: config.Config{
				FlagSets: []config.FlagSet{
					{
						APIKeys:         []string{"test"},
						PollingInterval: 1000,
						Retrievers: &[]retrieverconf.RetrieverConf{
							{
								Kind: "file",
								Path: testdataDir + "/config_flags.yaml",
							},
						},
					},
				},
			},
			apiKey:        "test",
			wantFile:      "flag_change_with_config_change.json",
			wantAfterFile: "flag_change_after_config_change.json",
		},
	}

	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			file, err := os.CreateTemp(t.TempDir(), "")
			require.NoError(t, err)
			defer func() {
				_ = file.Close()
				_ = os.Remove(file.Name())
			}()

			content, err := os.ReadFile(testdataDir + "/config_flags.yaml")
			require.NoError(t, err)

			errWF := os.WriteFile(file.Name(), content, 0644)
			require.NoError(t, errWF)
			file.Close()

			// Update the config to use the temp file
			if tt.config.Retrievers != nil {
				(*tt.config.Retrievers)[0].Path = file.Name()
			}
			if len(tt.config.FlagSets) > 0 {
				tt.config.FlagSets[0].Retrievers = &[]retrieverconf.RetrieverConf{
					{
						Kind: "file",
						Path: file.Name(),
					},
				}
			}

			flagsetManager, err := service.NewFlagsetManager(&tt.config, zap.NewNop(), []notifier.Notifier{}, nil, nil)
			require.NoError(t, err)
			defer flagsetManager.Close()

			ctrl := controller.NewAPIFlagChange(flagsetManager, metric.Metrics{})

			e := echo.New()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/v1/flag/change", nil)
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			if tt.apiKey != "" {
				req.Header.Set("Authorization", "Bearer "+tt.apiKey)
			}
			c := e.NewContext(req, rec)
			c.SetPath("/v1/flag/change")
			handlerErr := ctrl.Handler(c)
			require.NoError(t, handlerErr)

			want, _ := os.ReadFile(testdataDir + "/flag_change/" + tt.wantFile)
			assert.JSONEq(t, string(want), rec.Body.String())
			assert.Equal(t, http.StatusOK, rec.Code)

			content, err = os.ReadFile(testdataDir + "/config_flags_v2.yaml")
			require.NoError(t, err)

			errWF = os.WriteFile(file.Name(), content, 0644)
			require.NoError(t, errWF)

			// the poller of the retriever picks the new file up: wait for the answer to change
			wantAfter, err := os.ReadFile(testdataDir + "/flag_change/" + tt.wantAfterFile)
			require.NoError(t, err)
			var after string
			require.Eventually(t, func() bool {
				rec2 := httptest.NewRecorder()
				c2 := e.NewContext(req, rec2)
				c2.SetPath("/v1/flag/change")
				if handlerErr2 := ctrl.Handler(c2); handlerErr2 != nil {
					return false
				}
				after = rec2.Body.String()

				return rec2.Code == http.StatusOK && !jsonEqual(after, string(want))
			}, 30*time.Second, 50*time.Millisecond, "the answer never changed after the configuration change")
			assert.JSONEq(t, string(wantAfter), after)
		})
	}
}

func TestPIFlagChange_WithoutConfigChange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		apiKey   string
		wantFile string
		config   config.Config
	}{
		{
			name: "default mode",
			config: config.Config{
				CommonFlagSet: config.CommonFlagSet{
					PollingInterval: 1000,
					Retrievers: &[]retrieverconf.RetrieverConf{
						{
							Kind: "file",
							Path: testdataDir + "/config_flags.yaml",
						},
					},
				},
			},
			wantFile: "flag_change_without_config_change.json",
		},
		{
			name: "flagset mode",
			config: config.Config{
				FlagSets: []config.FlagSet{
					{
						APIKeys:         []string{"test"},
						PollingInterval: 1000,
						Retrievers: &[]retrieverconf.RetrieverConf{
							{
								Kind: "file",
								Path: testdataDir + "/config_flags.yaml",
							},
						},
					},
				},
			},
			apiKey:   "test",
			wantFile: "flag_change_without_config_change.json",
		},
	}

	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			file, err := os.CreateTemp(t.TempDir(), "")
			require.NoError(t, err)
			defer func() {
				_ = file.Close()
				_ = os.Remove(file.Name())
			}()

			content, err := os.ReadFile(testdataDir + "/config_flags.yaml")
			require.NoError(t, err)

			errWF := os.WriteFile(file.Name(), content, 0644)
			require.NoError(t, errWF)
			file.Close()

			// Update the config to use the temp file
			if tt.config.Retrievers != nil {
				(*tt.config.Retrievers)[0].Path = file.Name()
			}
			if len(tt.config.FlagSets) > 0 {
				tt.config.FlagSets[0].Retrievers = &[]retrieverconf.RetrieverConf{
					{
						Kind: "file",
						Path: file.Name(),
					},
				}
			}

			flagsetManager, err := service.NewFlagsetManager(&tt.config, zap.NewNop(), []notifier.Notifier{}, nil, nil)
			require.NoError(t, err)
			defer flagsetManager.Close()

			ctrl := controller.NewAPIFlagChange(flagsetManager, metric.Metrics{})

			e := echo.New()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/v1/flag/change", nil)
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			if tt.apiKey != "" {
				req.Header.Set("Authorization", "Bearer "+tt.apiKey)
			}
			c := e.NewContext(req, rec)
			c.SetPath("/v1/flag/change")
			handlerErr := ctrl.Handler(c)
			require.NoError(t, handlerErr)

			want, _ := os.ReadFile(
				testdataDir + "/flag_change/" + tt.wantFile,
			)
			assert.JSONEq(t, string(want), rec.Body.String())
			assert.Equal(t, http.StatusOK, rec.Code)

			time.Sleep(1500 * time.Millisecond)

			rec2 := httptest.NewRecorder()
			c2 := e.NewContext(req, rec2)
			c2.SetPath("/v1/flag/change")
			handlerErr2 := ctrl.Handler(c2)
			require.NoError(t, handlerErr2)
			assert.JSONEq(t, string(want), rec2.Body.String())
		})
	}
}

// jsonEqual reports whether a and b are the same JSON document.
func jsonEqual(a, b string) bool {
	var x, y any
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
		return false
	}

	return reflect.DeepEqual(x, y)
}
