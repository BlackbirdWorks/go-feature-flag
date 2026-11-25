package model_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/generate/manifest/model"
	helpermodel "github.com/blackbirdworks/semaphore/cmdhelpers/manifest/model"
)

func TestFlagManifest_MarshalJSON_includesSchema(t *testing.T) {
	t.Parallel()

	m := model.FlagManifest{Flags: map[string]helpermodel.FlagDefinition{}}
	raw, err := json.MarshalIndent(m, "", "  ")
	require.NoError(t, err)
	require.Contains(t, string(raw), `"$schema": "`+model.DefaultFlagManifestSchemaURL+`"`)
}
