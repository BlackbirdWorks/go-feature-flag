package semaphore_test

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	controller "github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/testdata/mock"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
	configFlagsYaml = "/config_flags.yaml"
	flagChangePath  = "/flag_change/"
)

func TestPIFlagChange_WithConfigChange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		apiKey        string
		wantFile      string
		wantAfterFile string
		named         bool
	}{
		{
			name:          fixtures.DefaultMode,
			wantFile:      "flag_change_with_config_change.json",
			wantAfterFile: "flag_change_after_config_change.json",
		},
		{
			name:          fixtures.FlagsetMode,
			named:         true,
			apiKey:        fixtures.PlaceholderTest,
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

			content, err := os.ReadFile(testdataDir + configFlagsYaml)
			require.NoError(t, err)

			errWF := os.WriteFile(file.Name(), content, 0o644)
			require.NoError(t, errWF)
			file.Close()

			cfg := mock.FileConfig(file.Name())
			cfg.PollingInterval = time.Second

			var flagsetManager *mock.FlagSets
			if tt.named {
				flagsetManager = mock.NewNamed(t, mock.Named{APIKeys: []string{fixtures.PlaceholderTest}, Config: cfg})
			} else {
				flagsetManager = mock.NewDefault(t, &cfg)
			}

			ctrl := controller.NewAPIFlagChange(flagsetManager, &metric.Metrics{})

			input := &controller.FlagChangeInput{Request: controller.NewRequest(tt.apiKey, "")}
			out, handlerErr := ctrl.Handle(t.Context(), input)
			require.NoError(t, handlerErr)

			want, _ := os.ReadFile(testdataDir + flagChangePath + tt.wantFile)
			assert.JSONEq(t, string(want), jsonOf(t, out.Body))

			content, err = os.ReadFile(testdataDir + "/config_flags_v2.yaml")
			require.NoError(t, err)

			errWF = os.WriteFile(file.Name(), content, 0o644)
			require.NoError(t, errWF)

			// the poller of the retriever picks the new file up: wait for the answer to change
			wantAfter, err := os.ReadFile(testdataDir + flagChangePath + tt.wantAfterFile)
			require.NoError(t, err)

			var after string

			require.Eventually(t, func() bool {
				out2, handlerErr2 := ctrl.Handle(t.Context(), input)
				if handlerErr2 != nil {
					return false
				}

				after = jsonOf(t, out2.Body)

				return !jsonEqual(after, string(want))
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
		named    bool
	}{
		{
			name:     fixtures.DefaultMode,
			wantFile: "flag_change_without_config_change.json",
		},
		{
			name:     fixtures.FlagsetMode,
			named:    true,
			apiKey:   fixtures.PlaceholderTest,
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

			content, err := os.ReadFile(testdataDir + configFlagsYaml)
			require.NoError(t, err)

			errWF := os.WriteFile(file.Name(), content, 0o644)
			require.NoError(t, errWF)
			file.Close()

			cfg := mock.FileConfig(file.Name())
			cfg.PollingInterval = time.Second

			var flagsetManager *mock.FlagSets
			if tt.named {
				flagsetManager = mock.NewNamed(t, mock.Named{APIKeys: []string{fixtures.PlaceholderTest}, Config: cfg})
			} else {
				flagsetManager = mock.NewDefault(t, &cfg)
			}

			ctrl := controller.NewAPIFlagChange(flagsetManager, &metric.Metrics{})

			input := &controller.FlagChangeInput{Request: controller.NewRequest(tt.apiKey, "")}
			out, handlerErr := ctrl.Handle(t.Context(), input)
			require.NoError(t, handlerErr)

			want, _ := os.ReadFile(
				testdataDir + flagChangePath + tt.wantFile,
			)
			assert.JSONEq(t, string(want), jsonOf(t, out.Body))

			time.Sleep(1500 * time.Millisecond)

			out2, handlerErr2 := ctrl.Handle(t.Context(), input)
			require.NoError(t, handlerErr2)
			assert.JSONEq(t, string(want), jsonOf(t, out2.Body))
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
