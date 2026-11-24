package signer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/internal/signer"
)

func TestSign(t *testing.T) {
	t.Parallel()

	type args struct {
		payloadBody []byte
		secretToken []byte
	}
	tests := []struct {
		name string
		want string
		args args
	}{
		{
			name: "sign valid",
			args: args{
				payloadBody: []byte("this is a test"),
				secretToken: []byte("secret"),
			},
			want: "sha256=08abbecc4779c9260cc85a017eb9db8babb5308a614cc9f13a4b9976af6b7cee",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := signer.Sign(tt.args.payloadBody, tt.args.secretToken)
			assert.Equal(t, tt.want, got)
		})
	}
}
