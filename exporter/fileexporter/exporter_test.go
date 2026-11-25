package fileexporter_test

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xitongsys/parquet-go-source/local"
	"github.com/xitongsys/parquet-go/parquet"
	"github.com/xitongsys/parquet-go/reader"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/exporter/fileexporter"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

type fileExportFields struct {
	Format                  string
	Filename                string
	CsvTemplate             string
	OutputDir               string
	ParquetCompressionCodec string
	EventType               string
}

type fileExportArgs struct {
	logger *fflog.FFLogger
	events []exporter.ExportableEvent
}

type fileExportExpected struct {
	fileNameRegex  string
	content        string
	featureEvents  []exporter.FeatureEvent
	trackingEvents []exporter.TrackingEvent
}

type fileExportCase struct {
	setup    func(t *testing.T, fields fileExportFields)
	teardown func(t *testing.T, fields fileExportFields)
	fields   fileExportFields
	name     string
	expected fileExportExpected
	args     fileExportArgs
	wantErr  bool
}

func runFileExportTests(t *testing.T, tests []fileExportCase) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			outputDir := tt.fields.OutputDir
			if tt.fields.OutputDir == "" {
				outputDir = t.TempDir()
			}

			if tt.setup != nil {
				tt.setup(t, tt.fields)
			}

			if tt.teardown != nil {
				defer tt.teardown(t, tt.fields)
			}

			fileExp := &fileexporter.Exporter{
				Format:                  tt.fields.Format,
				OutputDir:               outputDir,
				Filename:                tt.fields.Filename,
				CsvTemplate:             tt.fields.CsvTemplate,
				ParquetCompressionCodec: tt.fields.ParquetCompressionCodec,
			}

			err := fileExp.Export(context.Background(), tt.args.logger, tt.args.events)
			if tt.wantErr {
				assert.Error(t, err, "export method should error")

				return
			}

			require.NoError(t, err)

			// Check if the directory was created
			_, err = os.Stat(outputDir)
			require.NoError(t, err, "Output directory should exist")

			files, _ := os.ReadDir(outputDir)
			assert.Len(t, files, 1, "Directory %s should have only one file", outputDir)
			assert.Regexp(t, tt.expected.fileNameRegex, files[0].Name(), "Invalid file name")

			assertExportOutput(
				t,
				tt.fields.Format,
				tt.fields.EventType,
				outputDir+"/"+files[0].Name(),
				tt.expected.content,
				tt.expected.featureEvents,
				tt.expected.trackingEvents,
			)
		})
	}
}

func TestFile_Export(t *testing.T) {
	t.Parallel()

	hostname, _ := os.Hostname()
	runFileExportTests(t, []fileExportCase{
		{
			name:    "all default json",
			wantErr: false,
			fields:  fileExportFields{},
			args: fileExportArgs{
				events: []exporter.ExportableEvent{
					exporter.FeatureEvent{
						Kind:         "feature",
						ContextKind:  "anonymousUser",
						UserKey:      "ABCD",
						CreationDate: 1617970547,
						Key:          "random-key",
						Variation:    "Default",
						Value:        "YO",
						Default:      false,
						Source:       "SERVER",
					},
					exporter.FeatureEvent{
						Kind:         "feature",
						ContextKind:  "anonymousUser",
						UserKey:      "EFGH",
						CreationDate: 1617970701,
						Key:          "random-key",
						Variation:    "Default",
						Value:        "YO2",
						Default:      false,
						Version:      "127",
						Source:       "SERVER",
					},
				},
			},
			expected: fileExportExpected{
				fileNameRegex: "^flag-variation-" + hostname + "-[0-9]*\\.json$",
				content:       "./testdata/all_default.json",
			},
		},
		{
			name:    "all default csv",
			wantErr: false,
			fields: fileExportFields{
				Format: "csv",
			},
			args: fileExportArgs{
				events: []exporter.ExportableEvent{
					exporter.FeatureEvent{
						Kind:         "feature",
						ContextKind:  "anonymousUser",
						UserKey:      "ABCD",
						CreationDate: 1617970547,
						Key:          "random-key",
						Variation:    "Default",
						Value:        "YO",
						Default:      false,
						Source:       "SERVER",
					},
					exporter.FeatureEvent{
						Kind:         "feature",
						ContextKind:  "anonymousUser",
						UserKey:      "EFGH",
						CreationDate: 1617970701,
						Key:          "random-key",
						Variation:    "Default",
						Value:        "YO2",
						Default:      false,
						Source:       "SERVER",
					},
				},
			},
			expected: fileExportExpected{
				fileNameRegex: "^flag-variation-" + hostname + "-[0-9]*\\.csv",
				content:       "./testdata/all_default.csv",
			},
		},
		{
			name:    "all default parquet",
			wantErr: false,
			fields: fileExportFields{
				Format:                  "parquet",
				ParquetCompressionCodec: parquet.CompressionCodec_SNAPPY.String(),
			},
			args: fileExportArgs{
				events: []exporter.ExportableEvent{
					exporter.FeatureEvent{
						Kind:         "feature",
						ContextKind:  "anonymousUser",
						UserKey:      "ABCD",
						CreationDate: 1617970547,
						Key:          "random-key",
						Variation:    "Default",
						Value:        "YO",
						Default:      false,
						Source:       "SERVER",
						Metadata:     map[string]any{"test": "test"},
					},
					exporter.FeatureEvent{
						Kind:         "feature",
						ContextKind:  "anonymousUser",
						UserKey:      "EFGH",
						CreationDate: 1617970701,
						Key:          "random-key",
						Variation:    "Default",
						Value:        "YO2",
						Default:      false,
						Version:      "127",
						Source:       "SERVER",
					},
				},
			},
			expected: fileExportExpected{
				fileNameRegex: "^flag-variation-" + hostname + "-[0-9]*\\.parquet$",
				featureEvents: []exporter.FeatureEvent{
					{
						Kind:         "feature",
						ContextKind:  "anonymousUser",
						UserKey:      "ABCD",
						CreationDate: 1617970547,
						Key:          "random-key",
						Variation:    "Default",
						Value:        `"YO"`,
						Default:      false,
						Source:       "SERVER",
						Metadata:     map[string]any{"test": "test"},
					},
					{
						Kind:         "feature",
						ContextKind:  "anonymousUser",
						UserKey:      "EFGH",
						CreationDate: 1617970701,
						Key:          "random-key",
						Variation:    "Default",
						Value:        `"YO2"`,
						Default:      false,
						Version:      "127",
						Source:       "SERVER",
						Metadata:     map[string]any{},
					},
				},
			},
		},
	})
}

func TestFile_Export_FormatsAndEvents(t *testing.T) {
	t.Parallel()

	hostname, _ := os.Hostname()
	runFileExportTests(t, []fileExportCase{
		{
			name:    "all default parquet tracking events",
			wantErr: false,
			fields: fileExportFields{
				Format:                  "parquet",
				ParquetCompressionCodec: parquet.CompressionCodec_SNAPPY.String(),
				Filename:                "tracking-{{ .Hostname}}-{{ .Timestamp}}.parquet",
				EventType:               "tracking",
			},
			args: fileExportArgs{

				events: []exporter.ExportableEvent{
					exporter.TrackingEvent{
						Kind:              "feature",
						ContextKind:       "anonymous",
						UserKey:           "xxx",
						CreationDate:      1617970547,
						Key:               "what-ever-you-want",
						EvaluationContext: ffcontext.NewEvaluationContext("xxx-xxx-xxx").ToMap(),
						TrackingDetails:   map[string]any{"foo": "bar"},
					},
				},
			},
			expected: fileExportExpected{
				fileNameRegex: "^tracking-" + hostname + "-[0-9]*\\.parquet$",
				trackingEvents: []exporter.TrackingEvent{
					{
						Kind:              "feature",
						ContextKind:       "anonymous",
						UserKey:           "xxx",
						CreationDate:      1617970547,
						Key:               "what-ever-you-want",
						EvaluationContext: ffcontext.NewEvaluationContext("xxx-xxx-xxx").ToMap(),
						TrackingDetails:   map[string]any{"foo": "bar"},
					},
				},
			},
		},
		{
			name:    "custom CSV format",
			wantErr: false,
			fields: fileExportFields{
				Format:      "csv",
				CsvTemplate: "{{ .Kind}};{{ .ContextKind}}\n",
			},
			args: fileExportArgs{
				events: []exporter.ExportableEvent{
					exporter.FeatureEvent{
						Kind:         "feature",
						ContextKind:  "anonymousUser",
						UserKey:      "ABCD",
						CreationDate: 1617970547,
						Key:          "random-key",
						Variation:    "Default",
						Value:        "YO",
						Default:      false,
						Source:       "SERVER",
					},
					exporter.FeatureEvent{
						Kind:         "feature",
						ContextKind:  "anonymousUser",
						UserKey:      "EFGH",
						CreationDate: 1617970701,
						Key:          "random-key",
						Variation:    "Default",
						Value:        "YO2",
						Default:      false,
						Source:       "SERVER",
					},
				},
			},
			expected: fileExportExpected{
				fileNameRegex: "^flag-variation-" + hostname + "-[0-9]*\\.csv",
				content:       "./testdata/custom_csv_format.csv",
			},
		},
		{
			name:    "complex parquet value",
			wantErr: false,
			fields: fileExportFields{
				Format:                  "parquet",
				ParquetCompressionCodec: parquet.CompressionCodec_UNCOMPRESSED.String(),
			},
			args: fileExportArgs{
				events: []exporter.ExportableEvent{
					exporter.FeatureEvent{
						Kind:         "feature",
						ContextKind:  "anonymousUser",
						UserKey:      "ABCD",
						CreationDate: 1617970547,
						Key:          "random-key",
						Variation:    "Default",
						Value: map[string]any{
							"string": "string",
							"bool":   true,
							"float":  1.23,
							"int":    1,
						},
						Default:  false,
						Source:   "SERVER",
						Metadata: map[string]any{"test": "test"},
					},
				},
			},
			expected: fileExportExpected{
				fileNameRegex: "^flag-variation-" + hostname + "-[0-9]*\\.parquet$",
				featureEvents: []exporter.FeatureEvent{
					{
						Kind:         "feature",
						ContextKind:  "anonymousUser",
						UserKey:      "ABCD",
						CreationDate: 1617970547,
						Key:          "random-key",
						Variation:    "Default",
						Value:        `{"bool":true,"float":1.23,"int":1,"string":"string"}`,
						Default:      false,
						Source:       "SERVER",
						Metadata:     map[string]any{"test": "test"},
					},
				},
			},
		},
		{
			name:    "custom file name",
			wantErr: false,
			fields: fileExportFields{
				Format:   "json",
				Filename: "{{ .Format}}-test-{{ .Timestamp}}",
			},
			args: fileExportArgs{
				events: []exporter.ExportableEvent{
					exporter.FeatureEvent{
						Kind:         "feature",
						ContextKind:  "anonymousUser",
						UserKey:      "ABCD",
						CreationDate: 1617970547,
						Key:          "random-key",
						Variation:    "Default",
						Value:        "YO",
						Default:      false,
						Source:       "SERVER",
					},
					exporter.FeatureEvent{
						Kind:         "feature",
						ContextKind:  "anonymousUser",
						UserKey:      "EFGH",
						CreationDate: 1617970701,
						Key:          "random-key",
						Variation:    "Default",
						Value:        "YO2",
						Default:      false,
						Source:       "SERVER",
					},
				},
			},
			expected: fileExportExpected{
				fileNameRegex: "^json-test-[0-9]*$",
				content:       "./testdata/custom_file_name.json",
			},
		},
	})
}

func TestFile_Export_OutputDirAndErrors(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	hostname, _ := os.Hostname()
	runFileExportTests(t, []fileExportCase{
		{
			name:    "invalid format",
			wantErr: false,
			fields: fileExportFields{
				Format: "xxx",
			},
			args: fileExportArgs{
				events: []exporter.ExportableEvent{
					exporter.FeatureEvent{
						Kind:         "feature",
						ContextKind:  "anonymousUser",
						UserKey:      "ABCD",
						CreationDate: 1617970547,
						Key:          "random-key",
						Variation:    "Default",
						Value:        "YO",
						Default:      false,
						Source:       "SERVER",
					},
					exporter.FeatureEvent{
						Kind:         "feature",
						ContextKind:  "anonymousUser",
						UserKey:      "EFGH",
						CreationDate: 1617970701,
						Key:          "random-key",
						Variation:    "Default",
						Value:        "YO2",
						Default:      false,
						Version:      "127",
						Source:       "SERVER",
					},
				},
			},
			expected: fileExportExpected{
				fileNameRegex: "^flag-variation-" + hostname + "-[0-9]*\\.xxx$",
				content:       "./testdata/all_default.json",
			},
		},
		{
			name:    "non-existent outputdir",
			wantErr: false,
			fields: fileExportFields{
				OutputDir: filepath.Join(tempDir, "non-existent-dir"),
			},
			args: fileExportArgs{
				events: []exporter.ExportableEvent{
					exporter.FeatureEvent{
						Kind:         "feature",
						ContextKind:  "anonymousUser",
						UserKey:      "ABCD",
						CreationDate: 1617970547,
						Key:          "random-key",
						Variation:    "Default",
						Value:        "YO",
						Default:      false,
						Source:       "SERVER",
					},
					exporter.FeatureEvent{
						Kind:         "feature",
						ContextKind:  "anonymousUser",
						UserKey:      "EFGH",
						CreationDate: 1617970701,
						Key:          "random-key",
						Variation:    "Default",
						Value:        "YO2",
						Default:      false,
						Version:      "127",
						Source:       "SERVER",
					},
				},
			},
			expected: fileExportExpected{
				fileNameRegex: "^flag-variation-" + hostname + "-[0-9]*\\.json$",
				content:       "./testdata/all_default.json",
			},
		},
		{
			name:    "invalid filename template",
			wantErr: true,
			fields: fileExportFields{
				Filename: "{{ .InvalidField}}",
			},
			args: fileExportArgs{
				events: []exporter.ExportableEvent{
					exporter.FeatureEvent{
						Kind:         "feature",
						ContextKind:  "anonymousUser",
						UserKey:      "ABCD",
						CreationDate: 1617970547,
						Key:          "random-key",
						Variation:    "Default",
						Value:        "YO",
						Default:      false,
						Source:       "SERVER",
					},
					exporter.FeatureEvent{
						Kind:         "feature",
						ContextKind:  "anonymousUser",
						UserKey:      "EFGH",
						CreationDate: 1617970701,
						Key:          "random-key",
						Variation:    "Default",
						Value:        "YO2",
						Default:      false,
						Source:       "SERVER",
					},
				},
			},
		},
		{
			name:    "invalid csv formatter",
			wantErr: true,
			fields: fileExportFields{
				Format:      "csv",
				CsvTemplate: "{{ .Foo}}",
			},
			args: fileExportArgs{
				events: []exporter.ExportableEvent{
					exporter.FeatureEvent{
						Kind:         "feature",
						ContextKind:  "anonymousUser",
						UserKey:      "ABCD",
						CreationDate: 1617970547,
						Key:          "random-key",
						Variation:    "Default",
						Value:        "YO",
						Default:      false,
						Source:       "SERVER",
					},
					exporter.FeatureEvent{
						Kind:         "feature",
						ContextKind:  "anonymousUser",
						UserKey:      "EFGH",
						CreationDate: 1617970701,
						Key:          "random-key",
						Variation:    "Default",
						Value:        "YO2",
						Default:      false,
						Source:       "SERVER",
					},
				},
			},
		},
		{
			name:    "outputdir with invalid permissions",
			wantErr: true,
			fields: fileExportFields{
				Format:    "parquet",
				OutputDir: filepath.Join(tempDir, "invalid-permissions-dir"),
			},
			args: fileExportArgs{
				events: []exporter.ExportableEvent{
					exporter.FeatureEvent{
						Kind:         "feature",
						ContextKind:  "anonymousUser",
						UserKey:      "ABCD",
						CreationDate: 1617970547,
						Key:          "random-key",
						Variation:    "Default",
						Value:        "YO",
						Default:      false,
						Source:       "SERVER",
					},
				},
			},
			setup: func(t *testing.T, fields fileExportFields) {
				t.Helper()
				// Unix permission bits (Chmod 0000) do not restrict directory writes
				// on Windows, so the export would not fail there.
				if runtime.GOOS == "windows" {
					t.Skip("directory permissions are not enforced the same way on Windows")
				}

				err := os.MkdirAll(fields.OutputDir, 0755)
				require.NoError(t, err)
				err = os.Chmod(fields.OutputDir, 0000) // Remove all permissions
				assert.NoError(t, err)
			},
			teardown: func(t *testing.T, fields fileExportFields) {
				t.Helper()

				err := os.Chmod(fields.OutputDir, 0755) // Restore permissions for cleanup
				assert.NoError(t, err)
			},
		},
		{
			name:    "outputdir with invalid parent folder",
			wantErr: true,
			fields: fileExportFields{
				Format:    "parquet",
				OutputDir: filepath.Join(tempDir, "invalid-parent", "invalid-parent-dir"),
			},
			args: fileExportArgs{
				events: []exporter.ExportableEvent{
					exporter.FeatureEvent{
						Kind:         "feature",
						ContextKind:  "anonymousUser",
						UserKey:      "ABCD",
						CreationDate: 1617970547,
						Key:          "random-key",
						Variation:    "Default",
						Value:        "YO",
						Default:      false,
						Source:       "SERVER",
					},
				},
			},
			setup: func(t *testing.T, fields fileExportFields) {
				t.Helper()
				// Unix permission bits (Chmod 0000) do not restrict directory writes
				// on Windows, so the export would not fail there.
				if runtime.GOOS == "windows" {
					t.Skip("directory permissions are not enforced the same way on Windows")
				}
				// Only the dedicated parent of this case loses its permissions, the other
				// cases of this table run in parallel in sibling directories of tempDir.
				parent := filepath.Dir(fields.OutputDir)
				err := os.MkdirAll(parent, 0755)
				require.NoError(t, err)
				err = os.Chmod(parent, 0000) // Remove all permissions
				assert.NoError(t, err)
			},
			teardown: func(t *testing.T, fields fileExportFields) {
				t.Helper()

				err := os.Chmod(filepath.Dir(fields.OutputDir), 0755) // Restore permissions for cleanup
				assert.NoError(t, err)
			},
		},
		{
			name:    "outputdir with trailing slash",
			wantErr: false,
			fields: fileExportFields{
				Format:    "json",
				OutputDir: filepath.Join(tempDir, "dir-with-trailing-slash") + "/",
			},
			args: fileExportArgs{
				events: []exporter.ExportableEvent{
					exporter.FeatureEvent{
						Kind:         "feature",
						ContextKind:  "anonymousUser",
						UserKey:      "ABCD",
						CreationDate: 1617970547,
						Key:          "random-key",
						Variation:    "Default",
						Value:        "YO",
						Default:      false,
						Source:       "SERVER",
					},
					exporter.FeatureEvent{
						Kind:         "feature",
						ContextKind:  "anonymousUser",
						UserKey:      "EFGH",
						CreationDate: 1617970701,
						Key:          "random-key",
						Variation:    "Default",
						Value:        "YO2",
						Default:      false,
						Version:      "127",
						Source:       "SERVER",
					},
				},
			},
			expected: fileExportExpected{
				fileNameRegex: "^flag-variation-" + hostname + "-[0-9]*\\.json$",
				content:       "./testdata/all_default.json",
			},
		},
	})
}

// readParquetFeatureEvents reads the parquet file at filePath and returns the
// FeatureEvent records it contains.
func readParquetFeatureEvents(t *testing.T, filePath string) []exporter.FeatureEvent {
	t.Helper()

	fr, err := local.NewLocalFileReader(filePath)
	require.NoError(t, err)

	defer fr.Close()

	parquetReader, err := reader.NewParquetReader(fr, new(exporter.FeatureEvent), int64(runtime.NumCPU()))
	require.NoError(t, err)

	defer parquetReader.ReadStop()

	gotFeatureEvents := make([]exporter.FeatureEvent, parquetReader.GetNumRows())
	err = parquetReader.Read(&gotFeatureEvents)
	assert.NoError(t, err)

	return gotFeatureEvents
}

// readParquetTrackingEvents reads the parquet file at filePath and returns the
// TrackingEvent records it contains.
func readParquetTrackingEvents(t *testing.T, filePath string) []exporter.TrackingEvent {
	t.Helper()

	fr, err := local.NewLocalFileReader(filePath)
	require.NoError(t, err)

	defer fr.Close()

	parquetReader, err := reader.NewParquetReader(fr, new(exporter.TrackingEvent), int64(runtime.NumCPU()))
	require.NoError(t, err)

	defer parquetReader.ReadStop()

	gotTrackingEvents := make([]exporter.TrackingEvent, parquetReader.GetNumRows())
	err = parquetReader.Read(&gotTrackingEvents)
	assert.NoError(t, err)

	return gotTrackingEvents
}

// assertExportOutput validates the exported file. Parquet output is decoded and
// compared against the expected feature or tracking events depending on the
// event type; any other format is compared byte-for-byte against the expected
// content file.
func assertExportOutput(
	t *testing.T,
	format, eventType, filePath, expectedContentPath string,
	expectedFeatureEvents []exporter.FeatureEvent,
	expectedTrackingEvents []exporter.TrackingEvent,
) {
	t.Helper()

	if format == "parquet" {
		if eventType == "tracking" {
			assert.ElementsMatch(t, expectedTrackingEvents, readParquetTrackingEvents(t, filePath))

			return
		}

		assert.ElementsMatch(t, expectedFeatureEvents, readParquetFeatureEvents(t, filePath))

		return
	}

	expectedContent, _ := os.ReadFile(expectedContentPath)
	gotContent, _ := os.ReadFile(filePath)
	assert.Equal(
		t,
		string(expectedContent),
		string(gotContent),
		"Wrong content in the output file",
	)
}

func TestFile_IsBulk(t *testing.T) {
	t.Parallel()

	e := fileexporter.Exporter{}
	assert.True(t, e.IsBulk(), "DeprecatedExporterV1 is a bulk exporter")
}

func TestExportWithoutOutputDir(t *testing.T) {
	t.Parallel()

	featureEvents := []exporter.ExportableEvent{
		exporter.FeatureEvent{
			Kind: "feature", ContextKind: "anonymousUser", UserKey: "ABCD", CreationDate: 1617970547, Key: "random-key",
			Variation: "Default", Value: "YO", Default: false, Source: "SERVER",
		}}

	filePrefix := "test-flag-variation-EXAMPLE-"
	e := fileexporter.Exporter{
		Format:   "json",
		Filename: filePrefix + "{{ .Timestamp}}.{{ .Format}}",
	}
	err := e.Export(context.Background(), nil, featureEvents)
	require.NoError(t, err)

	// check that a file exist
	files, err := os.ReadDir("./")
	require.NoError(t, err)

	countFileWithPrefix := 0

	for _, file := range files {
		if strings.HasPrefix(file.Name(), filePrefix) {
			countFileWithPrefix++
			removeErr := os.Remove(path.Clean(file.Name()))
			require.NoError(t, removeErr)
		}
	}

	assert.Positive(t, countFileWithPrefix, "At least one file should have been created")
}
