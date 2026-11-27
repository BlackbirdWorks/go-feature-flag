package valkeysegment

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/valkey-io/valkey-go"
)

func TestBuildClientOption(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                 string
		opts                 Options
		wantBlockingPipeline int
		wantAlways           bool
		wantDisableAuto      bool
		wantDisableCache     bool
		wantDisableRetry     bool
	}{
		{
			name: "defaults are safe", wantAlways: true, wantDisableCache: true, wantDisableRetry: true,
			wantBlockingPipeline: math.MaxInt,
		},
		{
			name: "always", opts: Options{Pipelining: PipeliningAlways}, wantAlways: true,
			wantDisableCache: true, wantDisableRetry: true, wantBlockingPipeline: math.MaxInt,
		},
		{
			name: "auto is the library default", opts: Options{Pipelining: PipeliningAuto},
			wantDisableCache: true, wantDisableRetry: true, wantBlockingPipeline: math.MaxInt,
		},
		{
			name: "off", opts: Options{Pipelining: PipeliningOff}, wantDisableAuto: true,
			wantDisableCache: true, wantDisableRetry: true, wantBlockingPipeline: math.MaxInt,
		},
		{
			name: "client side cache", opts: Options{ClientSideCache: true}, wantAlways: true,
			wantDisableRetry: true, wantBlockingPipeline: math.MaxInt,
		},
		{
			name: "retry", opts: Options{Retry: true}, wantAlways: true, wantDisableCache: true,
			wantBlockingPipeline: math.MaxInt,
		},
		{
			name: "blocking pipeline threshold", opts: Options{BlockingPipeline: 500}, wantAlways: true,
			wantDisableCache: true, wantDisableRetry: true, wantBlockingPipeline: 500,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := buildClientOption(&valkey.ClientOption{}, &tt.opts)

			assert.Equal(t, tt.wantAlways, got.AlwaysPipelining)
			assert.Equal(t, tt.wantDisableAuto, got.DisableAutoPipelining)
			assert.Equal(t, tt.wantDisableCache, got.DisableCache)
			assert.Equal(t, tt.wantDisableRetry, got.DisableRetry)
			assert.Equal(t, tt.wantBlockingPipeline, got.BlockingPipeline)
		})
	}
}

func TestBuildClientOptionOverridesTheClientSettingsAndKeepsTheRest(t *testing.T) {
	t.Parallel()

	base := valkey.ClientOption{
		InitAddress: []string{"a:1"}, Username: "u", AlwaysPipelining: false, DisableAutoPipelining: true,
		DisableCache: false, DisableRetry: false, BlockingPipeline: 7,
	}
	got := buildClientOption(&base, &Options{})

	assert.Equal(t, []string{"a:1"}, got.InitAddress)
	assert.Equal(t, "u", got.Username)
	assert.True(t, got.AlwaysPipelining)
	assert.False(t, got.DisableAutoPipelining)
	assert.True(t, got.DisableCache)
	assert.True(t, got.DisableRetry)
	assert.Equal(t, math.MaxInt, got.BlockingPipeline)
	assert.Equal(t, 7, base.BlockingPipeline, "the base is not changed")
}
