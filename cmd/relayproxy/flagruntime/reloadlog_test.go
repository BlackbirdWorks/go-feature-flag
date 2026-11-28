package flagruntime_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/modules/core/clock"
)

// TestEngine_ReloadLog: one line per applied reload with the counts, one per rejected generation with its reason,
// none for an unchanged poll, and never a key.
func TestEngine_ReloadLog(t *testing.T) {
	t.Parallel()

	const (
		secret     = "LOG-SECRET-KEY"
		gen1       = "generation=1"
		outApplied = "outcome=applied"
	)

	withKey := func(t *testing.T, value int, keys ...string) objs {
		t.Helper()

		return objs{
			"f1":     flagsObj(value, "x", "y"),
			setsName: setsObj(t, fset("a", src("f1"), keys...)),
		}
	}

	tests := []struct {
		name      string
		contents  func(t *testing.T) []*flagruntime.Content
		wantLines [][]string
	}{
		{
			name: "created",
			contents: func(t *testing.T) []*flagruntime.Content {
				t.Helper()

				return []*flagruntime.Content{content(1, withKey(t, 1, secret))}
			},
			wantLines: [][]string{{
				outApplied, gen1, "flagsets=[created=1 updated=0 deleted=0]",
				"flags=[created=2 updated=0 deleted=0]", "objects=[ok=2 stale=0 missing=0 pending=0 invalid=0]",
				"rejected=0", "duration=0s",
			}},
		},
		{
			name: "unchanged poll",
			contents: func(t *testing.T) []*flagruntime.Content {
				t.Helper()

				return []*flagruntime.Content{content(1, withKey(t, 1, secret)), content(1, withKey(t, 1, secret))}
			},
			wantLines: [][]string{{outApplied, gen1}},
		},
		{
			name: "updated",
			contents: func(t *testing.T) []*flagruntime.Content {
				t.Helper()

				return []*flagruntime.Content{content(1, withKey(t, 1, secret)), content(2, withKey(t, 2, secret))}
			},
			wantLines: [][]string{{gen1}, {
				"generation=2", "flagsets=[created=0 updated=1 deleted=0]", "flags=[created=0 updated=2 deleted=0]",
			}},
		},
		{
			name: "deleted",
			contents: func(t *testing.T) []*flagruntime.Content {
				t.Helper()

				return []*flagruntime.Content{
					content(1, withKey(t, 1, secret)), content(2, objs{setsName: setsObj(t)}),
				}
			},
			wantLines: [][]string{{gen1}, {
				"generation=2", "flagsets=[created=0 updated=0 deleted=1]", "flags=[created=0 updated=0 deleted=2]",
			}},
		},
		{
			name: "rejected flag",
			contents: func(t *testing.T) []*flagruntime.Content {
				t.Helper()

				return []*flagruntime.Content{content(1, objs{
					"f1":     obj(flagruntime.KindFlags, join(flagsYAML(1, "x"), badYAML())),
					setsName: setsObj(t, fset("a", src("f1"), secret)),
				})}
			},
			wantLines: [][]string{{outApplied, "flags=[created=1 updated=0 deleted=0]", "rejected=1"}},
		},
		{
			name: "rejected generation",
			contents: func(t *testing.T) []*flagruntime.Content {
				t.Helper()

				return []*flagruntime.Content{content(0, withKey(t, 1, secret))}
			},
			wantLines: [][]string{{"outcome=rejected", "code=invalid-content", "generation=0", "detail="}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler := newRecordingHandler()
			rig := newRigLogger(t, slog.New(handler), flagruntime.Hooks{})
			rig.start()

			for _, c := range tt.contents(t) {
				rig.feed(c)
			}

			handler.mu.Lock()
			lines := append([]string(nil), *handler.records...)
			handler.mu.Unlock()

			require.Len(t, lines, len(tt.wantLines))

			for idx, want := range tt.wantLines {
				for _, part := range want {
					assert.Contains(t, lines[idx], part)
				}

				assert.NotContains(t, lines[idx], secret)
			}
		})
	}
}

// TestBuilder_ObjectSince: an object that is not ok carries the time of the build that first saw it so, kept while it
// stays not ok, and none once it is ok again.
func TestBuilder_ObjectSince(t *testing.T) {
	t.Parallel()

	start := clock.Now()
	stepTime := func(n int) time.Time { return start.Add(time.Duration(n) * time.Minute) }

	ok := flagsObj(1, "x")
	missing := flagruntime.Object{Kind: flagruntime.KindFlags, State: flagruntime.ObjectMissing, Reason: "gone"}
	pending := flagruntime.Object{Kind: flagruntime.KindFlags, State: flagruntime.ObjectPending, Reason: "late"}

	tests := []struct {
		name      string
		wantSince time.Time
		steps     []flagruntime.Object
	}{
		{name: "ok", steps: []flagruntime.Object{ok, ok}},
		{name: "left ok", steps: []flagruntime.Object{ok, missing}, wantSince: stepTime(1)},
		{name: "stays not ok", steps: []flagruntime.Object{ok, missing, pending}, wantSince: stepTime(1)},
		{name: "recovered", steps: []flagruntime.Object{ok, missing, ok}},
		{name: "again", steps: []flagruntime.Object{ok, missing, ok, pending}, wantSince: stepTime(3)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var now time.Time

			b := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{Now: func() time.Time { return now }})

			var built *flagruntime.Built

			for idx, step := range tt.steps {
				now = stepTime(idx)

				var err error

				built, err = b.Build(context.Background(), content(uint64(idx+1), objs{
					"f1":     step,
					setsName: setsObj(t, controlplane.FlagSet{Name: "a", Sources: src("f1"), APIKeys: []string{keyA}}),
				}))
				require.NoError(t, err)

				b.Commit(built)
			}

			got := built.Runtime.Status.Objects["f1"]
			require.NotNil(t, got)
			assert.True(t, tt.wantSince.Equal(got.Since), "since %v want %v", got.Since, tt.wantSince)
		})
	}
}

// TestNewEngine_LogLevel: the lines follow the level of the injected handler: a reload at a disabled level writes
// nothing, a rejection is a warning.
func TestNewEngine_LogLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		generation uint64
		level      slog.Level
		wantLines  int
	}{
		{name: "info applied", generation: 1, level: slog.LevelInfo, wantLines: 1},
		{name: "warn applied", generation: 1, level: slog.LevelWarn, wantLines: 0},
		{name: "warn rejected", generation: 0, level: slog.LevelWarn, wantLines: 1},
		{name: "error rejected", generation: 0, level: slog.LevelError, wantLines: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			logger := slog.New(slog.NewTextHandler(&out, &slog.HandlerOptions{Level: tt.level}))
			rig := newRigLogger(t, logger, flagruntime.Hooks{})
			rig.start()
			rig.feed(content(tt.generation, simpleObjs(t, 1)))

			assert.Equal(t, tt.wantLines, strings.Count(out.String(), "runtime reload"))
		})
	}
}
