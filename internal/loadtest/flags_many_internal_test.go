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
		name      string
		n         int
		wantFlags int
	}{
		{name: "no flag", n: 0, wantFlags: 0},
		{name: "one static flag", n: 1, wantFlags: 1},
		{name: "every kind of flag", n: 30, wantFlags: 30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			type flagYAML struct {
				DefaultRule struct {
					Percentage map[string]float64 `yaml:"percentage"`
					Variation  string             `yaml:"variation"`
				} `yaml:"defaultRule"`
				TrackEvents *bool           `yaml:"trackEvents"`
				Variations  map[string]bool `yaml:"variations"`
				Targeting   []struct {
					Query string `yaml:"query"`
				} `yaml:"targeting"`
			}

			var flags map[string]flagYAML
			require.NoError(t, yaml.Unmarshal([]byte(ManyFlagsYAML(tt.n)), &flags))
			require.Len(t, flags, tt.wantFlags)

			var static, targeted, percentage int

			for i := range tt.n {
				flagDef, ok := flags[fmt.Sprintf("flag-%d", i)]
				require.True(t, ok, "flag-%d", i)
				require.NotNil(t, flagDef.TrackEvents)
				assert.False(t, *flagDef.TrackEvents, "bulk benchmarks must not emit events")
				assert.Len(t, flagDef.Variations, 2)

				switch {
				case len(flagDef.Targeting) > 0:
					targeted++

					assert.Equal(t, fmt.Sprintf(`key eq "user-%d"`, i), flagDef.Targeting[0].Query)
				case len(flagDef.DefaultRule.Percentage) > 0:
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
