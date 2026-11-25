package kafka

import (
	"crypto/sha256"
	"crypto/sha512"
	"fmt"
	"hash"

	"github.com/xdg-go/scram"
)

// SHA256 is the scram.HashGeneratorFcn of SCRAM-SHA-256.
func SHA256() hash.Hash { return sha256.New() }

// SHA512 is the scram.HashGeneratorFcn of SCRAM-SHA-512.
func SHA512() hash.Hash { return sha512.New() }

// XDGSCRAMClient is a SCRAM client used to authenticate against Kafka.
type XDGSCRAMClient struct {
	*scram.Client
	*scram.ClientConversation
	scram.HashGeneratorFcn
}

// Begin starts a SCRAM exchange with the user name, password and authorization ID.
func (x *XDGSCRAMClient) Begin(userName, password, authzID string) error {
	var err error

	x.Client, err = x.NewClient(userName, password, authzID)
	if err != nil {
		return fmt.Errorf("create SCRAM client: %w", err)
	}

	x.ClientConversation = x.NewConversation()

	return nil
}

// Step processes a server challenge and returns the next client response.
func (x *XDGSCRAMClient) Step(challenge string) (string, error) {
	res, err := x.ClientConversation.Step(challenge)
	if err != nil {
		return "", fmt.Errorf("step SCRAM conversation: %w", err)
	}

	return res, nil
}

// Done reports whether the SCRAM exchange has completed.
func (x *XDGSCRAMClient) Done() bool {
	return x.ClientConversation.Done()
}
