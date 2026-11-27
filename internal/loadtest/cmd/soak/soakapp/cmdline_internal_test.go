package soakapp

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	targetLibrary  = "library"
	targetRelay    = "relay"
	singleTarget   = "-target"
	singleNopeFlag = "-nope"
	dashCPUProfile = "-cpu.pprof"
)

func defaultCLI() cli {
	return cli{
		Target:        targetLibrary,
		Endpoint:      "semaphore-eval",
		Protocol:      "http1",
		Exporter:      "discard",
		FlagKey:       "rule-bool-track",
		Duration:      60 * time.Second,
		Interval:      5 * time.Second,
		FlushMs:       100,
		MaxEvents:     10000,
		HeapTolerance: 0.25,
	}
}

func withCLIFields(fn func(*cli)) cli {
	c := defaultCLI()
	fn(&c)

	return c
}

func TestParseCommandLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		want    cli
		wantErr bool
	}{
		{
			name: "defaults",
			want: defaultCLI(),
		},
		{
			name: "all flags with double dash",
			args: []string{
				"--target=relay",
				"--endpoint", "ofrep-bulk",
				"--protocol=h2c",
				"--exporter", "slow",
				"--flag=custom-flag",
				"--cpuprofile=/tmp/cpu.pprof",
				"--duration=30s",
				"--interval=2s",
				"--flush-ms=50",
				"--max-events=500",
				"--heap-tolerance=0.5",
				"--workers=4",
				"--warmup=3",
			},
			want: cli{
				Target:        targetRelay,
				Endpoint:      "ofrep-bulk",
				Protocol:      "h2c",
				Exporter:      "slow",
				FlagKey:       "custom-flag",
				CPUProfile:    "/tmp/cpu.pprof",
				Duration:      30 * time.Second,
				Interval:      2 * time.Second,
				FlushMs:       50,
				MaxEvents:     500,
				HeapTolerance: 0.5,
				Workers:       4,
				Warmup:        3,
			},
		},
		{
			name: "legacy single-dash flags",
			args: []string{
				singleTarget, targetRelay,
				"-endpoint=ofrep-eval",
				"-duration", "10s",
				"-heap-tolerance=0.1",
				"-workers", "2",
			},
			want: withCLIFields(func(c *cli) {
				c.Target = targetRelay
				c.Endpoint = "ofrep-eval"
				c.Duration = 10 * time.Second
				c.HeapTolerance = 0.1
				c.Workers = 2
			}),
		},
		{
			name: "help long and short",
			args: []string{"-h"},
			want: withCLIFields(func(c *cli) { c.Help = true }),
		},
		{
			name: "help=false",
			args: []string{"--help=false"},
			want: defaultCLI(),
		},
		{
			name: "dash-prefixed profile filename and negative workers",
			args: []string{"-cpuprofile", dashCPUProfile, "--flag", singleTarget, "-workers", "-1"},
			want: withCLIFields(func(c *cli) {
				c.CPUProfile = dashCPUProfile
				c.FlagKey = singleTarget
				c.Workers = -1
			}),
		},
		{name: "unknown flag", args: []string{"--nope"}, wantErr: true},
		{name: "unknown single-dash flag", args: []string{singleNopeFlag}, wantErr: true},
		{name: "unknown flag after help", args: []string{"--help", "--nope"}, wantErr: true},
		{name: "invalid duration", args: []string{"--duration", "abc"}, wantErr: true},
		{name: "invalid workers", args: []string{"--workers", "abc"}, wantErr: true},
		{name: "unexpected positional arg after --", args: []string{"--", singleTarget}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseCommandLine(tt.args)
			if tt.wantErr {
				require.ErrorIs(t, err, errUsage)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNormalizeArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "single-dash value and help flags",
			args: []string{singleTarget, targetRelay, "-help", "-h", singleNopeFlag},
			want: []string{"--target", targetRelay, "--help", "-h", singleNopeFlag},
		},
		{
			name: "double-dash value starting with dash is joined with equals",
			args: []string{"--flag", singleTarget, "-cpuprofile", dashCPUProfile, "-endpoint=ofrep-eval"},
			want: []string{"--flag=-target", "--cpuprofile=" + dashCPUProfile, "--endpoint=ofrep-eval"},
		},
		{
			name: "double-dash terminator stops normalization",
			args: []string{singleTarget, targetLibrary, "--", "-endpoint", "x"},
			want: []string{"--target", targetLibrary, "--", "-endpoint", "x"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, normalizeArgs(tt.args))
		})
	}
}
