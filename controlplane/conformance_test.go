package controlplane_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
)

const vectors = "testdata/v1"

// vector is a decoded conformance vector.
type vector struct {
	manifest *controlplane.Manifest
	set      *controlplane.FlagSetDocument
	segments *controlplane.SegmentsDocument
	// manifestFile is the manifest that a flag-set vector is validated against
	manifestFile string
	flags        controlplane.Flags
	unknown      []string
}

// load decodes one vector. Flag-set vectors are resolved against valid/manifest-basic.yaml by validate.
func load(t *testing.T, path string) (*vector, error) {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	base := filepath.Base(path)
	format := strings.TrimPrefix(filepath.Ext(base), ".")
	out := &vector{manifest: nil, set: nil, segments: nil, flags: nil, unknown: nil}

	switch {
	case strings.HasPrefix(base, "manifest-"):
		var info controlplane.DecodeInfo

		out.manifest, info, err = controlplane.UnmarshalManifest(data)
		out.unknown = info.UnknownFields
	case strings.HasPrefix(base, "segments-"):
		var info controlplane.DecodeInfo

		out.segments, info, err = controlplane.UnmarshalSegments(data)
		out.unknown = info.UnknownFields
	case strings.HasPrefix(base, "flagset-"):
		var info controlplane.DecodeInfo

		out.set, info, err = controlplane.UnmarshalFlagSet(data)
		out.unknown = info.UnknownFields
	default:
		var info controlplane.DecodeInfo

		out.flags, info, err = controlplane.UnmarshalFlags(data, format)
		out.unknown = info.UnknownFields
	}

	out.manifestFile = "manifest-basic.yaml"
	if strings.HasPrefix(base, "flagset-segments") {
		out.manifestFile = "manifest-segments.yaml"
	}

	return out, err
}

func (v *vector) validate(t *testing.T) error {
	t.Helper()

	switch {
	case v.segments != nil:
		return controlplane.ValidateSegments(v.segments)
	case v.manifest != nil:
		return controlplane.ValidateManifest(v.manifest)
	case v.set != nil:
		raw, err := os.ReadFile(filepath.Join(vectors, "valid", v.manifestFile))
		require.NoError(t, err)

		manifest, _, err := controlplane.UnmarshalManifest(raw)
		require.NoError(t, err)

		return controlplane.ValidateFlagSet(v.set, manifest)
	default:
		return controlplane.ValidateFlags(v.flags)
	}
}

// marshal writes the vector back in YAML or JSON.
func (v *vector) marshal(asJSON bool) ([]byte, error) {
	switch {
	case v.manifest != nil && asJSON:
		return controlplane.MarshalManifestJSON(v.manifest)
	case v.manifest != nil:
		return controlplane.MarshalManifest(v.manifest)
	case v.segments != nil && asJSON:
		return controlplane.MarshalSegmentsJSON(v.segments)
	case v.segments != nil:
		return controlplane.MarshalSegments(v.segments)
	case v.set != nil && asJSON:
		return controlplane.MarshalFlagSetJSON(v.set)
	case v.set != nil:
		return controlplane.MarshalFlagSet(v.set)
	case asJSON:
		return controlplane.MarshalFlagsJSON(v.flags)
	default:
		return controlplane.MarshalFlags(v.flags)
	}
}

// check decodes and validates one vector the way the relay does and returns the unknown fields and the first
// error.
func check(t *testing.T, path string) ([]string, error) {
	t.Helper()

	decoded, err := load(t, path)
	if err != nil {
		return nil, err
	}

	return decoded.unknown, decoded.validate(t)
}

func vectorFiles(t *testing.T, dir string) []string {
	t.Helper()

	all, err := filepath.Glob(filepath.Join(vectors, dir, "*"))
	require.NoError(t, err)

	var files []string

	for _, file := range all {
		if ext := filepath.Ext(file); ext == extYAML || ext == extJSON {
			files = append(files, file)
		}
	}

	require.NotEmpty(t, files)

	return files
}

func TestConformanceValid(t *testing.T) {
	t.Parallel()

	for _, file := range vectorFiles(t, "valid") {
		t.Run(filepath.Base(file), func(t *testing.T) {
			t.Parallel()

			unknown, err := check(t, file)
			require.NoError(t, err)
			assert.Empty(t, unknown)
		})
	}
}

// TestConformanceValidRoundTrip writes every valid vector back in YAML and in JSON and reads it again: the
// result is the same document, and writing it twice gives the same bytes.
func TestConformanceValidRoundTrip(t *testing.T) {
	t.Parallel()

	for _, file := range vectorFiles(t, "valid") {
		t.Run(filepath.Base(file), func(t *testing.T) {
			t.Parallel()

			first, err := load(t, file)
			require.NoError(t, err)

			for _, asJSON := range []bool{false, true} {
				written, marshalErr := first.marshal(asJSON)
				require.NoError(t, marshalErr)

				path := filepath.Join(t.TempDir(), filepath.Base(file))
				if asJSON {
					path = strings.TrimSuffix(path, filepath.Ext(path)) + extJSON
				} else {
					path = strings.TrimSuffix(path, filepath.Ext(path)) + extYAML
				}

				require.NoError(t, os.WriteFile(path, written, 0o600))

				second, loadErr := load(t, path)
				require.NoError(t, loadErr)
				assert.Equal(t, first.manifest, second.manifest)
				assert.Equal(t, first.set, second.set)
				assert.Equal(t, first.segments, second.segments)
				assert.Equal(t, first.flags, second.flags)
				assert.Empty(t, second.unknown)

				again, againErr := second.marshal(asJSON)
				require.NoError(t, againErr)
				assert.Equal(t, string(written), string(again))
			}
		})
	}
}

func TestConformanceInvalid(t *testing.T) {
	t.Parallel()

	sentinels := map[string]error{
		"ErrWrongKind":             controlplane.ErrWrongKind,
		"ErrUnsupportedAPIVersion": controlplane.ErrUnsupportedAPIVersion,
		"ErrInvalid":               controlplane.ErrInvalid,
		"ErrDecode":                controlplane.ErrDecode,
	}

	for _, file := range vectorFiles(t, "invalid") {
		t.Run(filepath.Base(file), func(t *testing.T) {
			t.Parallel()

			side, err := os.ReadFile(file + ".error")
			require.NoError(t, err)

			lines := strings.Split(strings.TrimSpace(string(side)), "\n")
			require.Len(t, lines, 2)

			want, ok := sentinels[lines[0]]
			require.True(t, ok, "unknown error class %q", lines[0])

			_, err = check(t, file)
			require.ErrorIs(t, err, want)
			assert.Contains(t, err.Error(), lines[1])
		})
	}
}

func TestConformanceForwardCompatible(t *testing.T) {
	t.Parallel()

	for _, file := range vectorFiles(t, "forward-compatible") {
		t.Run(filepath.Base(file), func(t *testing.T) {
			t.Parallel()

			degraded, degradedErr := os.ReadFile(file + ".degraded")
			if degradedErr == nil {
				checkDegradedVector(t, file, degraded)

				return
			}

			require.ErrorIs(t, degradedErr, os.ErrNotExist)

			side, err := os.ReadFile(file + ".unknown")
			require.NoError(t, err)

			unknown, err := check(t, file)
			require.NoError(t, err)
			assert.Equal(t, strings.Fields(string(side)), unknown)
		})
	}
}

func checkDegradedVector(t *testing.T, file string, sidecar []byte) {
	t.Helper()

	var wantLines []string

	for line := range strings.SplitSeq(string(sidecar), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			wantLines = append(wantLines, trimmed)
		}
	}

	require.NotEmpty(t, wantLines)

	unknown, err := check(t, file)
	assert.Empty(t, unknown)

	valErr, ok := errors.AsType[*controlplane.ValidationError](err)
	require.True(t, ok, "expected *ValidationError, got %v", err)
	require.Len(t, valErr.Problems, len(wantLines))

	for i, line := range wantLines {
		fields := strings.Fields(line)
		require.Len(t, fields, 4)

		prob := valErr.Problems[i]
		assert.Equal(t, controlplane.EntryKind(fields[0]), prob.Kind)
		assert.Equal(t, fields[1], prob.Name)
		assert.Equal(t, controlplane.Code(fields[2]), prob.Code)
		assert.Equal(t, fields[3], prob.Path)
		assert.Equal(t, controlplane.EntryObject, prob.Kind)
		assert.Equal(t, controlplane.CodeUnsupportedVersion, prob.Code)
	}
}

// TestConformanceVectorsAreAllExercised fails when a vector directory is empty or thin, when a file is neither a
// vector nor the sidecar of one (a misspelled extension would otherwise never run), when a vector has no
// expectation sidecar or a sidecar has no vector, when a name has no known type prefix, and when the README
// does not mention a vector.
func TestConformanceVectorsAreAllExercised(t *testing.T) {
	t.Parallel()

	readme, err := os.ReadFile(filepath.Join(vectors, "README.md"))
	require.NoError(t, err)

	tests := []struct {
		dir      string
		sidecars []string
		min      int
	}{
		{"valid", nil, 15},
		{"invalid", []string{".error"}, 20},
		{"forward-compatible", []string{".unknown", ".degraded"}, 3},
	}

	for _, tt := range tests {
		t.Run(tt.dir, func(t *testing.T) {
			t.Parallel()

			entries, readErr := os.ReadDir(filepath.Join(vectors, tt.dir))
			require.NoError(t, readErr)

			names := map[string]bool{}
			for _, entry := range entries {
				names[entry.Name()] = true
			}

			loaded := 0

			for name := range names {
				ext := filepath.Ext(name)

				switch {
				case ext == extYAML || ext == extJSON:
					loaded++

					assert.Regexp(t, `^(manifest|flagset|segments|flags)-`, name, "a vector needs a type prefix")
					assert.Contains(t, string(readme), name, "the README must describe every vector")
					assertVectorSidecars(t, tt.dir, name, tt.sidecars, names)
				case slices.Contains(tt.sidecars, ext):
					assert.True(t, names[strings.TrimSuffix(name, ext)], "%s has no vector", name)
				default:
					assert.Fail(t, "not a vector and not a sidecar", name)
				}
			}

			assert.Len(t, vectorFiles(t, tt.dir), loaded, "every vector is loaded by the conformance tests")
			assert.GreaterOrEqual(t, loaded, tt.min)
		})
	}
}

func assertVectorSidecars(t *testing.T, dir, name string, sidecars []string, names map[string]bool) {
	t.Helper()

	if len(sidecars) == 0 {
		return
	}

	matches := 0

	for _, sideExt := range sidecars {
		if !names[name+sideExt] {
			continue
		}

		matches++

		side, sideErr := os.ReadFile(filepath.Join(vectors, dir, name+sideExt))
		require.NoError(t, sideErr)
		assert.NotEmpty(t, strings.TrimSpace(string(side)), "%s has an empty %s sidecar", name, sideExt)
	}

	assert.Equal(t, 1, matches, "%s must have one sidecar of %v", name, sidecars)
}
