package kafka_test

import (
	"crypto/sha256"
	"crypto/sha512"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config/kafka"
)

func TestHashGenerators(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		newHash  func() []byte
		wantSize int
	}{
		{name: "SHA256", newHash: func() []byte { return kafka.SHA256().Sum(nil) }, wantSize: sha256.Size},
		{name: "SHA512", newHash: func() []byte { return kafka.SHA512().Sum(nil) }, wantSize: sha512.Size},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Len(t, tt.newHash(), tt.wantSize)
		})
	}
}
