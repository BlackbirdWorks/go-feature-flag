package logsexporter_test

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/exporter/logsexporter"
	"github.com/blackbirdworks/semaphore/testhelpers"
	"github.com/blackbirdworks/semaphore/testhelpers/slogutil"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

const (
	aBCD             = "ABCD"
	anonymousUser    = "anonymousUser"
	feature          = "feature"
	variationDefault = "Default"
	randomKey        = "random-key"
)

func TestLog_Export(t *testing.T) {
	t.Parallel()

	type fields struct {
		LogFormat string
	}

	type args struct {
		featureEvents []exporter.ExportableEvent
	}

	tests := []struct {
		name        string
		fields      fields
		expectedLog string
		args        args
		wantErr     bool
	}{
		{
			name:   "Default format",
			fields: fields{LogFormat: ""},
			args: args{featureEvents: []exporter.ExportableEvent{
				exporter.FeatureEvent{
					Kind:         feature,
					ContextKind:  anonymousUser,
					UserKey:      aBCD,
					CreationDate: 1617970547,
					Key:          randomKey,
					Variation:    variationDefault,
					Value:        "YO",
					Default:      false,
				},
			}},
			expectedLog: "user=\"ABCD\", flag=\"random-key\", value=\"YO\"\n",
		},
		{
			name: "Custom format",
			fields: fields{
				LogFormat: "key=\"{{ .Key}}\"",
			},
			args: args{featureEvents: []exporter.ExportableEvent{
				exporter.FeatureEvent{
					Kind:         feature,
					ContextKind:  anonymousUser,
					UserKey:      aBCD,
					CreationDate: 1617970547,
					Key:          randomKey,
					Variation:    variationDefault,
					Value:        "YO",
					Default:      false,
				},
			}},
			expectedLog: "key=\"random-key\"\n",
		},
		{
			name: "LogFormat error",
			fields: fields{
				LogFormat: "key=\"{{ .Key}\" [{{ .FormattedDate}}]",
			},
			args: args{featureEvents: []exporter.ExportableEvent{
				exporter.FeatureEvent{
					Kind:         feature,
					ContextKind:  anonymousUser,
					UserKey:      aBCD,
					CreationDate: 1617970547,
					Key:          randomKey,
					Variation:    variationDefault,
					Value:        "YO",
					Default:      false,
				},
			}},
			expectedLog: "^\\[" + testhelpers.RFC3339Regex + "\\] user=\"ABCD\", flag=\"random-key\", value=\"YO\"\n",
		},
		{
			name: "Field does not exist",
			fields: fields{
				LogFormat: "key=\"{{ .UnknownKey}}\" [{{ .FormattedDate}}]",
			},
			args: args{featureEvents: []exporter.ExportableEvent{
				exporter.FeatureEvent{
					Kind:         feature,
					ContextKind:  anonymousUser,
					UserKey:      aBCD,
					CreationDate: 1617970547,
					Key:          randomKey,
					Variation:    variationDefault,
					Value:        "YO",
					Default:      false,
				},
			}},
			expectedLog: "^\\[" + testhelpers.RFC3339Regex + "\\] user=\"ABCD\", flag=\"random-key\", value=\"YO\"\n",
			wantErr:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logsExp := &logsexporter.Exporter{
				LogFormat: tt.fields.LogFormat,
			}

			logFile, _ := os.CreateTemp(t.TempDir(), "")
			textHandler := slogutil.MessageOnlyHandler{Writer: logFile}
			logger := &fflog.FFLogger{LeveledLogger: slog.New(&textHandler)}
			err := logsExp.Export(context.Background(), logger, tt.args.featureEvents)

			if tt.wantErr {
				assert.Error(t, err, "It should return an error")

				return
			}

			require.NoError(t, err, "DeprecatedExporterV1 should not throw errors")

			logContent, _ := os.ReadFile(logFile.Name())
			assert.Regexp(t, tt.expectedLog, string(logContent))
		})
	}
}

func TestLog_IsBulk(t *testing.T) {
	t.Parallel()

	exp := logsexporter.Exporter{}
	assert.False(t, exp.IsBulk(), "File exporter is not a bulk exporter")
}
