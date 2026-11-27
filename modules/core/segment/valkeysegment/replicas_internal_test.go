package valkeysegment

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/valkey-io/valkey-go"
)

// A snapshot read (SSCAN, GET) is never sent to a replica; the other commands keep the choice of the caller.
func TestWhichCommandsAreSnapshotReads(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		words     []string
		wantAbove bool
	}{
		{name: "SSCAN", words: []string{"SSCAN", "k", "0"}, wantAbove: true},
		{name: "GET", words: []string{"GET", "k"}, wantAbove: true},
		{name: "SMISMEMBER", words: []string{"SMISMEMBER", "k", "a"}},
		{name: "EXISTS", words: []string{"EXISTS", "k"}},
		{name: "no word", words: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.wantAbove, isSnapshotWords(tt.words))
		})
	}
}

func TestTheReplicaChoiceOfTheCallerIsWrappedOnlyWhenThereIsOne(t *testing.T) {
	t.Parallel()

	none := buildClientOption(&valkey.ClientOption{}, &Options{})
	assert.Nil(t, none.SendToReplicas, "no replica routing stays none")

	wrapped := buildClientOption(
		&valkey.ClientOption{SendToReplicas: func(valkey.Completed) bool { return true }},
		&Options{},
	)
	assert.NotNil(t, wrapped.SendToReplicas)
}
