//go:build docker

package s3exporterv2_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xitongsys/parquet-go-source/local"
	"github.com/xitongsys/parquet-go/reader"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/exporter/s3exporterv2"
	"github.com/blackbirdworks/semaphore/testutils/gopherstack"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

func events(n int) []exporter.ExportableEvent {
	res := make([]exporter.ExportableEvent, 0, n)
	for i := range n {
		res = append(res, exporter.FeatureEvent{
			Kind:         "feature",
			ContextKind:  "user",
			UserKey:      fmt.Sprintf("user-%d", i),
			CreationDate: int64(1617970547 + i),
			Key:          "my-flag",
			Variation:    "enabled",
			Value:        fmt.Sprintf("value-%d", i),
			Default:      false,
			Source:       "SERVER",
		})
	}

	return res
}

func TestE2E_Export(t *testing.T) {
	t.Parallel()
	srv := gopherstack.Connect(t)
	hostname, _ := os.Hostname()

	tests := []struct {
		check        func(t *testing.T, content []byte, nbEvents int)
		name         string
		format       string
		s3Path       string
		filename     string
		csvTemplate  string
		wantKeyRegex string
		nbEvents     int
	}{
		{
			name: "json default format", format: "", s3Path: "exports/json", nbEvents: 3,
			wantKeyRegex: "^exports/json/flag-variation-" + regexp.QuoteMeta(hostname) + `-[0-9]+\.json$`,
			check:        checkJSON,
		},
		{
			name: "json explicit with custom filename", format: "JSON", s3Path: "a/b/c", nbEvents: 5,
			filename:     "custom-{{ .Hostname }}-{{ .Timestamp }}.{{ .Format }}",
			wantKeyRegex: "^a/b/c/custom-" + regexp.QuoteMeta(hostname) + `-[0-9]+\.json$`,
			check:        checkJSON,
		},
		{
			name: "empty S3Path gives a key starting with a slash", format: "json", s3Path: "", nbEvents: 1,
			wantKeyRegex: "^/flag-variation-" + regexp.QuoteMeta(hostname) + `-[0-9]+\.json$`,
			check:        checkJSON,
		},
		{
			name: "csv default template", format: "csv", s3Path: "exports/csv", nbEvents: 4,
			wantKeyRegex: "^exports/csv/flag-variation-" + regexp.QuoteMeta(hostname) + `-[0-9]+\.csv$`,
			check: func(t *testing.T, content []byte, n int) {
				t.Helper()

				lines := strings.Split(strings.TrimSpace(string(content)), "\n")
				require.Len(t, lines, n)

				for i, line := range lines {
					assert.Equal(
						t,
						fmt.Sprintf(
							"feature;user;user-%d;%d;my-flag;enabled;value-%d;false;SERVER",
							i,
							1617970547+i,
							i,
						),
						line,
					)
				}
			},
		},
		{
			name: "csv custom template and fixed filename", format: "csv", s3Path: "exports/csv2", nbEvents: 2,
			filename: "fixed.csv", csvTemplate: "{{ .UserKey }},{{ .Key }},{{ .Value }}\n",
			wantKeyRegex: "^exports/csv2/fixed.csv$",
			check: func(t *testing.T, content []byte, _ int) {
				t.Helper()
				assert.Equal(t, "user-0,my-flag,value-0\nuser-1,my-flag,value-1\n", string(content))
			},
		},
		{
			name: "parquet", format: "parquet", s3Path: "exports/parquet", nbEvents: 6,
			wantKeyRegex: "^exports/parquet/flag-variation-" + regexp.QuoteMeta(hostname) + `-[0-9]+\.parquet$`,
			check: func(t *testing.T, content []byte, count int) {
				t.Helper()
				f := filepath.Join(t.TempDir(), "out.parquet")
				require.NoError(t, os.WriteFile(f, content, 0o600))
				fr, err := local.NewLocalFileReader(f)
				require.NoError(t, err)

				defer func() { _ = fr.Close() }()

				parquetReader, err := reader.NewParquetReader(fr, new(exporter.FeatureEvent), int64(runtime.NumCPU()))
				require.NoError(t, err)

				defer parquetReader.ReadStop()

				require.Equal(t, int64(count), parquetReader.GetNumRows())
				got := make([]exporter.FeatureEvent, parquetReader.GetNumRows())
				require.NoError(t, parquetReader.Read(&got))

				for i, e := range got {
					assert.Equal(t, fmt.Sprintf("user-%d", i), e.UserKey)
					assert.Equal(t, "my-flag", e.Key)
					assert.Equal(t, fmt.Sprintf(`"value-%d"`, i), e.Value) // parquet stores the JSON encoding
					assert.Equal(t, "enabled", e.Variation)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			bucket := srv.CreateBucket(t, "exp")
			cfg := srv.MustAwsConfig(t)
			e := &s3exporterv2.Exporter{
				Bucket: bucket, AwsConfig: &cfg, Format: tt.format, S3Path: tt.s3Path,
				Filename: tt.filename, CsvTemplate: tt.csvTemplate,
			}
			require.NoError(t, e.Export(context.Background(), &fflog.FFLogger{}, events(tt.nbEvents)))

			keys := srv.ListKeys(t, bucket, "")
			require.Len(t, keys, 1, "keys: %v", keys)
			assert.Regexp(t, tt.wantKeyRegex, keys[0])
			tt.check(t, srv.GetObject(t, bucket, keys[0]), tt.nbEvents)
		})
	}
}

func checkJSON(t *testing.T, content []byte, n int) {
	t.Helper()

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	require.Len(t, lines, n)

	for i, l := range lines {
		var e exporter.FeatureEvent
		require.NoError(t, json.Unmarshal([]byte(l), &e))
		assert.Equal(t, fmt.Sprintf("user-%d", i), e.UserKey)
		assert.Equal(t, "my-flag", e.Key)
		assert.Equal(t, fmt.Sprintf("value-%d", i), e.Value)
		assert.Equal(t, "enabled", e.Variation)
	}
}

func TestE2E_Export_Errors(t *testing.T) {
	t.Parallel()
	srv := gopherstack.Connect(t)

	tests := []struct {
		name    string
		bucket  func(t *testing.T) string
		wantErr string
	}{
		{
			name: "missing bucket",
			bucket: func(t *testing.T) string {
				t.Helper()

				return gopherstack.UniqueName(t, "nobucket")
			},
			wantErr: "NoSuchBucket: The specified bucket does not exist.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := srv.MustAwsConfig(t)
			e := &s3exporterv2.Exporter{Bucket: tt.bucket(t), AwsConfig: &cfg, S3Path: "x"}
			require.ErrorContains(t, e.Export(context.Background(), &fflog.FFLogger{}, events(1)), tt.wantErr)
		})
	}
}

func TestE2E_Export_EachCallCreatesAFile(t *testing.T) {
	t.Parallel()
	srv := gopherstack.Connect(t)
	bucket := srv.CreateBucket(t, "multi")
	cfg := srv.MustAwsConfig(t)
	e := &s3exporterv2.Exporter{Bucket: bucket, AwsConfig: &cfg, S3Path: "p"}
	// distinct fixed names: the default timestamp-based name may collide within the same second
	for _, name := range []string{"one.json", "two.json", "three.json"} {
		e.Filename = name
		require.NoError(t, e.Export(context.Background(), &fflog.FFLogger{}, events(2)))
	}

	assert.ElementsMatch(t, []string{"p/one.json", "p/two.json", "p/three.json"}, srv.ListKeys(t, bucket, "p/"))
}
