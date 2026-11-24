package loadtest

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestManyFlagsYAML(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		n    int
	}{
		{name: "no flag", n: 0},
		{name: "one static flag", n: 1},
		{name: "every kind of flag", n: 30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var flags map[string]struct {
				TrackEvents *bool                    `yaml:"trackEvents"`
				Variations  map[string]bool          `yaml:"variations"`
				Targeting   []struct{ Query string } `yaml:"targeting"`
				DefaultRule struct {
					Variation  string             `yaml:"variation"`
					Percentage map[string]float64 `yaml:"percentage"`
				} `yaml:"defaultRule"`
			}
			require.NoError(t, yaml.Unmarshal([]byte(ManyFlagsYAML(tt.n)), &flags))
			require.Len(t, flags, tt.n)
			var static, targeted, percentage int
			for i := 0; i < tt.n; i++ {
				f, ok := flags[fmt.Sprintf("flag-%d", i)]
				require.True(t, ok, "flag-%d", i)
				require.NotNil(t, f.TrackEvents)
				assert.False(t, *f.TrackEvents, "bulk benchmarks must not emit events")
				assert.Len(t, f.Variations, 2)
				switch {
				case len(f.Targeting) > 0:
					targeted++
					assert.Equal(t, fmt.Sprintf(`key eq "user-%d"`, i), f.Targeting[0].Query)
				case len(f.DefaultRule.Percentage) > 0:
					percentage++
				default:
					static++
				}
			}
			assert.Equal(t, (tt.n+2)/3, static)
			assert.Equal(t, (tt.n+1)/3, targeted)
			assert.Equal(t, tt.n/3, percentage)
		})
	}
}
