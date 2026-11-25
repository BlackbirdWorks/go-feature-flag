package s3exporterv2

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/internal/protocol"
	"github.com/blackbirdworks/semaphore/testhelpers"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

const (
	exportShouldNotError = "Export should not error"
	flagVariation        = "^/flag-variation-"
)

type s3ExportFields struct {
	AwsConfig       *aws.Config
	Bucket          string
	Format          string
	S3Path          string
	Filename        string
	CsvTemplate     string
	S3ClientOptions []func(*s3.Options)
	nilCtx          bool
}

type s3ExportCase struct {
	name         string
	expectedFile string
	expectedName string
	events       []exporter.ExportableEvent
	fields       s3ExportFields
	wantErr      bool
}

func runS3ExportTests(t *testing.T, tests []s3ExportCase) {
	t.Helper()

	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s3ManagerMock := testhelpers.S3ManagerV2Mock{}
			s3Exp := &Exporter{
				Bucket:          tt.fields.Bucket,
				AwsConfig:       tt.fields.AwsConfig,
				Format:          tt.fields.Format,
				S3Path:          tt.fields.S3Path,
				Filename:        tt.fields.Filename,
				CsvTemplate:     tt.fields.CsvTemplate,
				S3ClientOptions: tt.fields.S3ClientOptions,
				s3Uploader:      &s3ManagerMock,
			}

			// Verify that S3ClientOptions are correctly set on the Exporter
			if tt.fields.S3ClientOptions != nil {
				assert.Equal(
					t,
					tt.fields.S3ClientOptions,
					s3Exp.S3ClientOptions,
					"S3ClientOptions should be set correctly on the Exporter",
				)
			}

			ctx := t.Context()
			if tt.fields.nilCtx {
				ctx = nil
			}

			err := s3Exp.Export(
				ctx,
				&fflog.FFLogger{LeveledLogger: slog.Default()},
				tt.events,
			)
			if tt.wantErr {
				assert.Error(t, err, "Export should error")

				return
			}

			require.NoError(t, err, exportShouldNotError)
			assert.Len(
				t,
				s3ManagerMock.S3ManagerMockFileSystem, 1,
				"we should have 1 file in our mock",
			)

			expectedContent, _ := os.ReadFile(tt.expectedFile)
			for k, v := range s3ManagerMock.S3ManagerMockFileSystem {
				assert.Equal(t, string(expectedContent), v, "invalid file content")
				assert.Regexp(t, tt.expectedName, k, "invalid file name")
			}
		})
	}
}

func TestS3_Export(t *testing.T) {
	t.Parallel()

	hostname, _ := os.Hostname()
	runS3ExportTests(t, []s3ExportCase{
		{
			name: "All default test",
			fields: s3ExportFields{
				Bucket: fixtures.Test,
			},
			events: []exporter.ExportableEvent{
				exporter.FeatureEvent{
					Kind:         fixtures.Feature,
					ContextKind:  fixtures.UserKeyAnonymous,
					UserKey:      fixtures.UserKeyABCD,
					CreationDate: 1617970547,
					Key:          fixtures.UserKeyRandom,
					Variation:    fixtures.VariationDefault,
					Value:        "YO",
					Default:      false,
					Source:       fixtures.SERVER,
				},
			},
			expectedFile: fixtures.AllDefaultJSONPath,
			expectedName: flagVariation + hostname + fixtures.JSONFileSuffixPattern,
		},
		{
			name: "All default test with nil context",
			fields: s3ExportFields{
				Bucket: fixtures.Test,
				nilCtx: true,
			},
			events: []exporter.ExportableEvent{
				exporter.FeatureEvent{
					Kind:         fixtures.Feature,
					ContextKind:  fixtures.UserKeyAnonymous,
					UserKey:      fixtures.UserKeyABCD,
					CreationDate: 1617970547,
					Key:          fixtures.UserKeyRandom,
					Variation:    fixtures.VariationDefault,
					Value:        "YO",
					Default:      false,
					Source:       fixtures.SERVER,
				},
			},
			expectedFile: fixtures.AllDefaultJSONPath,
			expectedName: flagVariation + hostname + fixtures.JSONFileSuffixPattern,
		},
		{
			name: "With DeprecatedExporterV1 Path",
			fields: s3ExportFields{
				S3Path: "random/path",
				Bucket: fixtures.Test,
			},
			events: []exporter.ExportableEvent{
				exporter.FeatureEvent{
					Kind:         fixtures.Feature,
					ContextKind:  fixtures.UserKeyAnonymous,
					UserKey:      fixtures.UserKeyABCD,
					CreationDate: 1617970547,
					Key:          fixtures.UserKeyRandom,
					Variation:    fixtures.VariationDefault,
					Value:        "YO",
					Default:      false,
					Source:       fixtures.SERVER,
				},
			},
			expectedFile: fixtures.AllDefaultJSONPath,
			expectedName: "^random/path/flag-variation-" + hostname + fixtures.JSONFileSuffixPattern,
		},
		{
			name: "All default CSV",
			fields: s3ExportFields{
				Format: protocol.FormatCSV,
				Bucket: fixtures.Test,
			},
			events: []exporter.ExportableEvent{
				exporter.FeatureEvent{
					Kind:         fixtures.Feature,
					ContextKind:  fixtures.UserKeyAnonymous,
					UserKey:      fixtures.UserKeyABCD,
					CreationDate: 1617970547,
					Key:          fixtures.UserKeyRandom,
					Variation:    fixtures.VariationDefault,
					Value:        "YO",
					Default:      false,
					Source:       fixtures.SERVER,
				},
			},
			expectedFile: "./testdata/all_default.csv",
			expectedName: flagVariation + hostname + "-[0-9]*\\.csv$",
		},
		{
			name: "Custom CSV",
			fields: s3ExportFields{
				Format:      protocol.FormatCSV,
				CsvTemplate: "{{ .Kind}};{{ .ContextKind}}\n",
				Bucket:      fixtures.Test,
			},
			events: []exporter.ExportableEvent{
				exporter.FeatureEvent{
					Kind:         fixtures.Feature,
					ContextKind:  fixtures.UserKeyAnonymous,
					UserKey:      fixtures.UserKeyABCD,
					CreationDate: 1617970547,
					Key:          fixtures.UserKeyRandom,
					Variation:    fixtures.VariationDefault,
					Value:        "YO",
					Default:      false,
					Source:       fixtures.SERVER,
				},
			},
			expectedFile: "./testdata/custom_csv_format.csv",
			expectedName: flagVariation + hostname + "-[0-9]*\\.csv$",
		},
		{
			name: "Custom FileName",
			fields: s3ExportFields{
				Format:   "json",
				Filename: "{{ .Format}}-test-{{ .Timestamp}}",
				Bucket:   fixtures.Test,
			},
			events: []exporter.ExportableEvent{
				exporter.FeatureEvent{
					Kind:         fixtures.Feature,
					ContextKind:  fixtures.UserKeyAnonymous,
					UserKey:      fixtures.UserKeyABCD,
					CreationDate: 1617970547,
					Key:          fixtures.UserKeyRandom,
					Variation:    fixtures.VariationDefault,
					Value:        "YO",
					Default:      false,
					Source:       fixtures.SERVER,
				},
			},
			expectedFile: fixtures.AllDefaultJSONPath,
			expectedName: "^/json-test-[0-9]*$",
		},
	})
}

func TestS3_Export_ErrorsAndOptions(t *testing.T) {
	t.Parallel()

	hostname, _ := os.Hostname()
	runS3ExportTests(t, []s3ExportCase{
		{
			name: "Invalid format",
			fields: s3ExportFields{
				Format: "xxx",
				Bucket: fixtures.Test,
			},
			events: []exporter.ExportableEvent{
				exporter.FeatureEvent{
					Kind:         fixtures.Feature,
					ContextKind:  fixtures.UserKeyAnonymous,
					UserKey:      fixtures.UserKeyABCD,
					CreationDate: 1617970547,
					Key:          fixtures.UserKeyRandom,
					Variation:    fixtures.VariationDefault,
					Value:        "YO",
					Default:      false,
					Source:       fixtures.SERVER,
				},
			},
			expectedFile: fixtures.AllDefaultJSONPath,
			expectedName: flagVariation + hostname + "-[0-9]*\\.xxx$",
		},
		{
			name: "Empty Bucket",
			fields: s3ExportFields{
				Format: "xxx",
			},
			events: []exporter.ExportableEvent{
				exporter.FeatureEvent{
					Kind:         fixtures.Feature,
					ContextKind:  fixtures.UserKeyAnonymous,
					UserKey:      fixtures.UserKeyABCD,
					CreationDate: 1617970547,
					Key:          fixtures.UserKeyRandom,
					Variation:    fixtures.VariationDefault,
					Value:        "YO",
					Default:      false,
					Source:       fixtures.SERVER,
				},
			},
			wantErr: true,
		},
		{
			name: "Invalid filename template",
			fields: s3ExportFields{
				Filename: "{{ .InvalidField}}",
				Bucket:   fixtures.Test,
			},
			events: []exporter.ExportableEvent{
				exporter.FeatureEvent{
					Kind:         fixtures.Feature,
					ContextKind:  fixtures.UserKeyAnonymous,
					UserKey:      fixtures.UserKeyABCD,
					CreationDate: 1617970547,
					Key:          fixtures.UserKeyRandom,
					Variation:    fixtures.VariationDefault,
					Value:        "YO",
					Default:      false,
					Source:       fixtures.SERVER,
				},
			},
			wantErr: true,
		},
		{
			name: "Invalid csv formatter",
			fields: s3ExportFields{
				Format:      protocol.FormatCSV,
				CsvTemplate: "{{ .Foo}}",
			},
			events: []exporter.ExportableEvent{
				exporter.FeatureEvent{
					Kind:         fixtures.Feature,
					ContextKind:  fixtures.UserKeyAnonymous,
					UserKey:      fixtures.UserKeyABCD,
					CreationDate: 1617970547,
					Key:          fixtures.UserKeyRandom,
					Variation:    fixtures.VariationDefault,
					Value:        "YO",
					Default:      false,
					Source:       fixtures.SERVER,
				},
			},
			wantErr: true,
		},
		{
			name: "With S3 Client Options",
			fields: s3ExportFields{
				Bucket: fixtures.Test,
				S3ClientOptions: []func(*s3.Options){
					func(o *s3.Options) {
						o.UseAccelerate = true
					},
				},
			},
			events: []exporter.ExportableEvent{
				exporter.FeatureEvent{
					Kind:         fixtures.Feature,
					ContextKind:  fixtures.UserKeyAnonymous,
					UserKey:      fixtures.UserKeyABCD,
					CreationDate: 1617970547,
					Key:          fixtures.UserKeyRandom,
					Variation:    fixtures.VariationDefault,
					Value:        "YO",
					Default:      false,
					Source:       fixtures.SERVER,
				},
			},
			expectedFile: fixtures.AllDefaultJSONPath,
			expectedName: flagVariation + hostname + fixtures.JSONFileSuffixPattern,
		},
	})
}

func Test_errSDK(t *testing.T) {
	t.Parallel()

	f := &Exporter{
		Bucket:    "empty",
		AwsConfig: &aws.Config{},
	}
	err := f.Export(
		context.Background(),
		&fflog.FFLogger{LeveledLogger: slog.Default()},
		[]exporter.ExportableEvent{},
	)
	assert.ErrorContains(t, err, "resolve endpoint: endpoint rule error, Invalid region")
}

// TestS3_Export_LogsThroughParameterLogger ensures the exporter logs through the
// logger passed to Export() and not through an always-nil internal field.
// See https://github.com/thomaspoignant/go-feature-flag/issues/5637.
func TestS3_Export_LogsThroughParameterLogger(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	logger := &fflog.FFLogger{
		LeveledLogger: slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})),
	}

	s3ManagerMock := testhelpers.S3ManagerV2Mock{}
	f := &Exporter{
		Bucket:     fixtures.Test,
		s3Uploader: &s3ManagerMock,
	}

	err := f.Export(
		context.TODO(),
		logger,
		[]exporter.ExportableEvent{
			exporter.FeatureEvent{
				Kind:         fixtures.Feature,
				ContextKind:  fixtures.UserKeyAnonymous,
				UserKey:      fixtures.UserKeyABCD,
				CreationDate: 1617970547,
				Key:          fixtures.UserKeyRandom,
				Variation:    fixtures.VariationDefault,
				Value:        "YO",
				Default:      false,
				Source:       fixtures.SERVER,
			},
		},
	)

	require.NoError(t, err, exportShouldNotError)
	assert.Contains(
		t,
		buf.String(),
		"[S3Exporter] file uploaded.",
		"the upload log line should be emitted through the logger passed to Export()",
	)
}

// TestS3_Export_RemovesTempDir ensures Export() fully removes the temporary
// directory it creates. It guards against the [os.Remove] -> [os.RemoveAll] fix:
// [os.Remove] only deletes empty directories, so with the exported files still
// inside the temp dir would leak on every export cycle.
// See https://github.com/thomaspoignant/go-feature-flag/issues/5636.
func TestS3_Export_RemovesTempDir(t *testing.T) {
	t.Parallel()

	//nolint:usetesting // Export creates its directory in the process temp dir; the test globs exactly that directory
	tempPattern := filepath.Join(os.TempDir(), "go_feature_flag_s3_export*")
	before, _ := filepath.Glob(tempPattern)

	f := &Exporter{Bucket: fixtures.Test, s3Uploader: &testhelpers.S3ManagerV2Mock{}}
	err := f.Export(
		context.TODO(),
		&fflog.FFLogger{LeveledLogger: slog.Default()},
		[]exporter.ExportableEvent{
			exporter.FeatureEvent{
				Kind: fixtures.Feature, ContextKind: fixtures.UserKeyAnonymous, UserKey: fixtures.UserKeyABCD,
				CreationDate: 1617970547, Key: fixtures.UserKeyRandom, Variation: fixtures.VariationDefault,
				Value: "YO", Default: false, Source: fixtures.SERVER,
			},
		},
	)
	require.NoError(t, err, exportShouldNotError)

	after, _ := filepath.Glob(tempPattern)
	assert.Len(
		t,
		after, len(before),
		"Export() must remove its temp directory (os.RemoveAll)",
	)
}

func TestS3_IsBulk(t *testing.T) {
	t.Parallel()

	exp := Exporter{}
	assert.True(t, exp.IsBulk(), "DeprecatedExporterV1 is a bulk exporter")
}
