package folderloader_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/folderloader"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

func TestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		itemName  string
		item      string
		prefix    string
		pattern   string
		wantErr   string
		recursive bool
	}{
		{name: "item only", item: fixtures.AYAML},
		{name: "nothing is the bucket root"},
		{name: "prefix only", prefix: fixtures.FlagsDirSlash},
		{name: "root prefix recursive", prefix: "/", recursive: true},
		{name: "prefix with pattern", prefix: fixtures.FlagsDirSlash, pattern: fixtures.YAMLGlob, recursive: true},
		{
			name:    "item and prefix",
			item:    fixtures.AYAML,
			prefix:  "f/",
			wantErr: "item and prefix are mutually exclusive",
		},
		{
			name: "custom item name", itemName: "object", item: fixtures.AYAML, prefix: "f/",
			wantErr: "object and prefix are mutually exclusive",
		},
		{
			name: "custom item name with pattern", itemName: "object", item: fixtures.AYAML, pattern: "*",
			wantErr: "pattern and recursive can only be used without object",
		},
		{
			name: "item with pattern", item: fixtures.AYAML, pattern: fixtures.YAMLGlob,
			wantErr: "pattern and recursive can only be used without item",
		},
		{
			name: "item with recursive", item: fixtures.AYAML, recursive: true,
			wantErr: "pattern and recursive can only be used without item",
		},
		{name: "invalid pattern", prefix: "f/", pattern: "[a-", wantErr: `invalid pattern "[a-"`},
		{name: "pattern with a slash", prefix: "f/", pattern: "sub/*.yaml", wantErr: `can't contain "/"`},
		{name: "pattern with a leading slash", pattern: "/a.yaml", wantErr: `can't contain "/"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.itemName == "" {
				tt.itemName = "item"
			}

			err := folderloader.Validate(tt.itemName, tt.item, tt.prefix, tt.pattern, tt.recursive)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)

				return
			}

			assert.NoError(t, err)
		})
	}
}
