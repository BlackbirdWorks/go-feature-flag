package notifier_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/notifier"
)

const (
	editor       = "jane"
	lineDisabled = "Flag disabled"
)

// wrapped is a flag that wraps an internal flag, as the relay does for a flag that uses segments.
type wrapped struct {
	*flagdef.InternalFlag
}

func (w wrapped) Internal() *flagdef.InternalFlag { return w.InternalFlag }

// The notifiers read the internal flag inside a wrapper: its editor and the changes between two flags.
func TestWrapper(t *testing.T) {
	t.Parallel()

	meta := map[string]any{notifier.UpdatedByMetadataKey: editor}
	enabled := &flagdef.InternalFlag{Metadata: &meta}
	disabled := &flagdef.InternalFlag{Metadata: &meta, Disable: new(true)}
	renamed := map[string]any{notifier.UpdatedByMetadataKey: "john"}
	other := &flagdef.InternalFlag{Metadata: &renamed}

	tests := []struct {
		before, after flagdef.Flag
		name          string
		wantEditor    string
		wantLines     []string
	}{
		{
			name: "not wrapped", before: enabled, after: disabled, wantEditor: editor,
			wantLines: []string{lineDisabled},
		},
		{
			name: "wrapped", before: wrapped{enabled}, after: wrapped{disabled}, wantEditor: editor,
			wantLines: []string{lineDisabled},
		},
		{
			name: "wrapped, other editor, no change", before: wrapped{enabled}, after: wrapped{other},
			wantEditor: editor, wantLines: nil,
		},
		{
			name: "one side wrapped", before: enabled, after: wrapped{disabled}, wantEditor: editor,
			wantLines: []string{lineDisabled},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.wantEditor, notifier.UpdatedBy(tt.before))

			lines, err := notifier.DescribeChanges(tt.before, tt.after)
			require.NoError(t, err)
			assert.Equal(t, tt.wantLines, lines)
		})
	}
}
