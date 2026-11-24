package model_test

import (
	"encoding/json"
	"testing"

	"github.com/blackbirdworks/semaphore/cmd/cli/generate/manifest/model"
	helperModel "github.com/blackbirdworks/semaphore/cmdhelpers/manifest/model"
	"github.com/stretchr/testify/require"
)

func TestFlagManifest_MarshalJSON_includesSchema(t *testing.T) {
	m := model.FlagManifest{Flags: map[string]helperModel.FlagDefinition{}}
	raw, err := json.MarshalIndent(m, "", "  ")
	require.NoError(t, err)
	require.Contains(t, string(raw), `"$schema": "`+model.DefaultFlagManifestSchemaURL+`"`)
}
