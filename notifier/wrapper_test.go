package notifier_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/notifier"
)

const editor = "jane"

// wrapped is a flag that wraps another one, as the relay does for a flag that uses segments.
type wrapped struct {
	*flagdef.InternalFlag
}

func (w wrapped) Internal() *flagdef.InternalFlag { return w.InternalFlag }

// The notifiers read the internal flag inside a wrapper.
func TestWrapper(t *testing.T) {
	t.Parallel()

	meta := map[string]any{notifier.UpdatedByMetadataKey: editor}
	inner := &flagdef.InternalFlag{Metadata: &meta}
	other := &flagdef.InternalFlag{Metadata: &meta, Disable: new(true)}

	tests := []struct {
		flag       flagdef.Flag
		name       string
		wantEditor string
	}{
		{name: "not wrapped", flag: inner, wantEditor: editor},
		{name: "wrapped", flag: wrapped{inner}, wantEditor: editor},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.wantEditor, notifier.UpdatedBy(tt.flag))

			lines, err := notifier.DescribeChanges(wrapped{inner}, wrapped{other})
			require.NoError(t, err)
			assert.NotEmpty(t, lines)
		})
	}
}
