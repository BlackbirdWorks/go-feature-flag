package controlplane_test

import (
	"os"
	"path/filepath"
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
	flags    controlplane.Flags
	unknown  []string
}

// load decodes one vector. Flag-set vectors are resolved against valid/manifest-basic.yaml by validate.
func load(t *testing.T, path string) (*vector, error) {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	base := filepath.Base(path)
	format := strings.TrimPrefix(filepath.Ext(base), ".")
	out := &vector{manifest: nil, set: nil, flags: nil, unknown: nil}

	switch {
	case strings.HasPrefix(base, "manifest-"):
		var info controlplane.DecodeInfo

		out.manifest, info, err = controlplane.UnmarshalManifest(data)
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

	return out, err
}

func (v *vector) validate(t *testing.T) error {
	t.Helper()

	switch {
	case v.manifest != nil:
		return controlplane.ValidateManifest(v.manifest)
	case v.set != nil:
		raw, err := os.ReadFile(filepath.Join(vectors, "valid", "manifest-basic.yaml"))
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

			side, err := os.ReadFile(file + ".unknown")
			require.NoError(t, err)

			unknown, err := check(t, file)
			require.NoError(t, err)
			assert.Equal(t, strings.Fields(string(side)), unknown)
		})
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
		dir     string
		sidecar string
		min     int
	}{
		{"valid", "", 15},
		{"invalid", ".error", 20},
		{"forward-compatible", ".unknown", 3},
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

					assert.Regexp(t, `^(manifest|flagset|flags)-`, name, "a vector needs a type prefix")
					assert.Contains(t, string(readme), name, "the README must describe every vector")

					if tt.sidecar != "" {
						assert.True(t, names[name+tt.sidecar], "%s has no %s sidecar", name, tt.sidecar)

						side, sideErr := os.ReadFile(filepath.Join(vectors, tt.dir, name+tt.sidecar))
						require.NoError(t, sideErr)
						assert.NotEmpty(
							t,
							strings.TrimSpace(string(side)),
							"%s has an empty %s sidecar",
							name,
							tt.sidecar,
						)
					}
				case tt.sidecar != "" && ext == tt.sidecar:
					assert.True(t, names[strings.TrimSuffix(name, tt.sidecar)], "%s has no vector", name)
				default:
					assert.Fail(t, "not a vector and not a sidecar", name)
				}
			}

			assert.Len(t, vectorFiles(t, tt.dir), loaded, "every vector is loaded by the conformance tests")
			assert.GreaterOrEqual(t, loaded, tt.min)
		})
	}
}
