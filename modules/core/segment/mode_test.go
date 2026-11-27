package segment_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

const (
	segAlpha   = "alpha"
	segGhost   = "ghost"
	wantBounds = "between 100ms and 24h"
)

func TestDefinitionModeAndVersion(t *testing.T) {
	t.Parallel()

	base := segment.Definition{Name: segAlpha, Type: segment.TypeRedis}
	tooLong := string(make([]byte, segment.MaxVersionLen+1))

	tests := []struct {
		version *segment.VersionSpec
		name    string
		mode    segment.Mode
		wantErr string
	}{
		{name: "no mode, no version"},
		{name: "snapshot", mode: segment.ModeSnapshot},
		{name: "lookup", mode: segment.ModeLookup},
		{name: "unknown mode lists the valid ones", mode: "stream", wantErr: `want snapshot or lookup, got "stream"`},
		{name: "zero spec", version: &segment.VersionSpec{}},
		{name: "value", version: &segment.VersionSpec{Value: "v7"}},
		{name: "key", version: &segment.VersionSpec{Key: "seg:alpha:version"}},
		{name: "key and interval", version: &segment.VersionSpec{Key: "k", Interval: time.Second}},
		{
			name:    "interval at the lower bound",
			version: &segment.VersionSpec{Key: "k", Interval: segment.MinVersionInterval},
		},
		{
			name:    "interval at the upper bound",
			version: &segment.VersionSpec{Key: "k", Interval: segment.MaxVersionInterval},
		},
		{name: "value and key", version: &segment.VersionSpec{Value: "v", Key: "k"}, wantErr: "mutually exclusive"},
		{
			name:    "interval without key",
			version: &segment.VersionSpec{Interval: time.Second},
			wantErr: "interval needs a key",
		},
		{
			name: "interval with value", version: &segment.VersionSpec{Value: "v", Interval: time.Second},
			wantErr: "interval needs a key",
		},
		{
			name: "interval too short", version: &segment.VersionSpec{Key: "k", Interval: time.Millisecond},
			wantErr: wantBounds,
		},
		{
			name: "interval too long", version: &segment.VersionSpec{Key: "k", Interval: 25 * time.Hour},
			wantErr: wantBounds,
		},
		{
			name: "negative interval", version: &segment.VersionSpec{Key: "k", Interval: -time.Second},
			wantErr: wantBounds,
		},
		{name: "value too long", version: &segment.VersionSpec{Value: tooLong}, wantErr: "longer than 512"},
		{name: "key too long", version: &segment.VersionSpec{Key: tooLong}, wantErr: "longer than 512"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			def := base
			def.Mode, def.Version = tt.mode, tt.version

			err := def.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, segment.ErrInvalidDefinition)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestModeEffective(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mode      segment.Mode
		wantMode  segment.Mode
		wantValid bool
	}{
		{mode: "", wantMode: segment.ModeSnapshot, wantValid: true},
		{mode: segment.ModeSnapshot, wantMode: segment.ModeSnapshot, wantValid: true},
		{mode: segment.ModeLookup, wantMode: segment.ModeLookup, wantValid: true},
		{mode: "other", wantMode: "other", wantValid: false},
	}

	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.wantMode, tt.mode.Effective())
			assert.Equal(t, tt.wantValid, tt.mode.Valid())
		})
	}
}

// lookupOnly is a store that serves lookup and nothing else.
type lookupOnly struct{}

func (lookupOnly) Contains(context.Context, []segment.Lookup) error { return nil }

func TestRequireMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		store   any
		name    string
		mode    segment.Mode
		wantErr string
	}{
		{name: "lookup on a provider", store: lookupOnly{}, mode: segment.ModeLookup},
		{name: "snapshot on a source", store: segment.NewMemorySource(nil), mode: segment.ModeSnapshot},
		{name: "default is snapshot, source ok", store: segment.NewMemorySource(nil), mode: ""},
		{
			name: "snapshot on a lookup-only store", store: lookupOnly{}, mode: segment.ModeSnapshot,
			wantErr: `segment "alpha": mode "snapshot", store "redis"`,
		},
		{
			name: "default on a lookup-only store names snapshot", store: lookupOnly{}, mode: "",
			wantErr: `mode "snapshot"`,
		},
		{name: "lookup on a non provider", store: struct{}{}, mode: segment.ModeLookup, wantErr: `mode "lookup"`},
		{name: "an unknown mode is unsupported", store: lookupOnly{}, mode: "stream", wantErr: `mode "stream"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := segment.RequireMode(segAlpha, tt.mode, "redis", tt.store)
			if tt.wantErr == "" {
				require.NoError(t, err)
				assert.True(t, segment.SupportsMode(tt.store, tt.mode))

				return
			}

			require.ErrorIs(t, err, segment.ErrUnsupportedMode)
			require.ErrorContains(t, err, tt.wantErr)
			assert.False(t, segment.SupportsMode(tt.store, tt.mode))
		})
	}
}

func TestRequireVersioner(t *testing.T) {
	t.Parallel()

	require.NoError(t, segment.RequireVersioner(segAlpha, "redis", segment.NewMemorySource(nil)))

	err := segment.RequireVersioner(segAlpha, "file", lookupOnly{})
	require.ErrorIs(t, err, segment.ErrUnsupportedMode)
	require.ErrorContains(t, err, `segment "alpha"`)
	assert.ErrorContains(t, err, `store "file"`)
}

func TestDefinitionMissingKeyAndLayout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		def     segment.Definition
		wantErr string
	}{
		{name: "defaults", def: segment.Definition{Name: segAlpha, Type: segment.TypeRedis}},
		{
			name: "missing notMember",
			def:  segment.Definition{Name: segAlpha, Type: segment.TypeRedis, MissingKey: segment.MissingNotMember},
		},
		{
			name: "missing unknown",
			def:  segment.Definition{Name: segAlpha, Type: segment.TypeRedis, MissingKey: segment.MissingUnknown},
		},
		{
			name:    "missing invalid",
			def:     segment.Definition{Name: segAlpha, Type: segment.TypeRedis, MissingKey: "nope"},
			wantErr: `want notMember or unknown, got "nope"`,
		},
		{
			name: "set layout, snapshot",
			def:  segment.Definition{Name: segAlpha, Type: segment.TypeRedis, Layout: segment.LayoutSet},
		},
		{
			name: "keys layout, lookup",
			def: segment.Definition{
				Name:   segAlpha,
				Type:   segment.TypeRedis,
				Layout: segment.LayoutKeys,
				Mode:   segment.ModeLookup,
			},
		},
		{
			name: "keys layout, explicit snapshot",
			def: segment.Definition{
				Name: segAlpha, Type: segment.TypeRedis, Layout: segment.LayoutKeys, Mode: segment.ModeSnapshot,
			},
			wantErr: `"alpha": layout "keys" with mode "snapshot": the keys layout is lookup-only`,
		},
		{
			name:    "keys layout, default mode is snapshot",
			def:     segment.Definition{Name: segAlpha, Type: segment.TypeRedis, Layout: segment.LayoutKeys},
			wantErr: `layout "keys" with mode "snapshot": the keys layout is lookup-only`,
		},
		{
			name: "layout invalid", def: segment.Definition{Name: segAlpha, Type: segment.TypeRedis, Layout: "tree"},
			wantErr: `want set or keys, got "tree"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.def.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, segment.ErrInvalidDefinition)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}

	keys := segment.Definition{Name: segAlpha, Type: segment.TypeRedis, Layout: segment.LayoutKeys}
	require.ErrorIs(t, keys.Validate(), segment.ErrKeysSnapshot)
	assert.Equal(t, segment.MissingNotMember, segment.MissingKey("").Effective())
}
