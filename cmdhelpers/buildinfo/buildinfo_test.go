package buildinfo_test

import (
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/cmdhelpers/buildinfo"
)

const (
	versionZeroOneZero = "0.1.0"
	go1274             = "go1.27.4"
)

func TestText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		bi     *debug.BuildInfo
		linked buildinfo.Linked
		name   string
		want   string
		fips   bool
	}{
		{
			name:   "release build uses the link flags",
			linked: buildinfo.Linked{Version: versionZeroOneZero, Commit: "abc123", Date: "2026-11-24T12:00:00Z"},
			bi:     &debug.BuildInfo{GoVersion: go1274, Main: debug.Module{Version: "(devel)"}},
			want:   "semaphore 0.1.0\ncommit: abc123\nbuilt: 2026-11-24T12:00:00Z\ngo: go1.27.4\nfips140: false\n",
		},
		{
			name:   "fips mode is reported",
			linked: buildinfo.Linked{Version: versionZeroOneZero, Commit: "abc123", Date: "d"},
			bi:     &debug.BuildInfo{GoVersion: go1274},
			fips:   true,
			want:   "semaphore 0.1.0\ncommit: abc123\nbuilt: d\ngo: go1.27.4\nfips140: true\n",
		},
		{
			name:   "go install build falls back to the module version and vcs settings",
			linked: buildinfo.Linked{Version: buildinfo.DevVersion},
			bi: &debug.BuildInfo{
				GoVersion: go1274,
				Main:      debug.Module{Version: "v0.1.0"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "def456"},
					{Key: "vcs.time", Value: "2026-11-23T00:00:00Z"},
				},
			},
			want: "semaphore v0.1.0\ncommit: def456\nbuilt: 2026-11-23T00:00:00Z\ngo: go1.27.4\nfips140: false\n",
		},
		{
			name:   "local build keeps the dev version and reports unknown values",
			linked: buildinfo.Linked{Version: buildinfo.DevVersion},
			bi:     &debug.BuildInfo{GoVersion: go1274, Main: debug.Module{Version: "(devel)"}},
			want:   "semaphore localdev\ncommit: unknown\nbuilt: unknown\ngo: go1.27.4\nfips140: false\n",
		},
		{
			name:   "link flags win over the vcs settings",
			linked: buildinfo.Linked{Version: "1.0.0", Commit: "linked", Date: "linked-date"},
			bi: &debug.BuildInfo{
				GoVersion: go1274,
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "vcs"},
					{Key: "vcs.time", Value: "vcs-date"},
				},
			},
			want: "semaphore 1.0.0\ncommit: linked\nbuilt: linked-date\ngo: go1.27.4\nfips140: false\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, buildinfo.Text("semaphore", tt.linked, tt.bi, tt.fips))
		})
	}
}

func TestTextWithoutBuildInfo(t *testing.T) {
	t.Parallel()

	got := buildinfo.Text("semaphore-cli", buildinfo.Linked{Version: versionZeroOneZero}, nil, false)
	assert.Contains(t, got, "semaphore-cli 0.1.0\ncommit: unknown\nbuilt: unknown\ngo: go")
}

func TestRead(t *testing.T) {
	t.Parallel()

	// Test binaries carry module build information.
	assert.NotNil(t, buildinfo.Read())
}
