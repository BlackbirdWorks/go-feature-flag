package semaphore_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// jsonOf returns the JSON document a response body is sent as.
func jsonOf(t *testing.T, v any) string {
	t.Helper()

	b, err := json.Marshal(v)
	require.NoError(t, err)

	return string(b)
}
