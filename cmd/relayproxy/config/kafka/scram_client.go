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

type XDGSCRAMClient struct {
	*scram.Client
	*scram.ClientConversation
	scram.HashGeneratorFcn
}

func (x *XDGSCRAMClient) Begin(userName, password, authzID string) error {
	var err error
	x.Client, err = x.NewClient(userName, password, authzID)
	if err != nil {
		return fmt.Errorf("create SCRAM client: %w", err)
	}
	x.ClientConversation = x.NewConversation()

	return nil
}

func (x *XDGSCRAMClient) Step(challenge string) (string, error) {
	res, err := x.ClientConversation.Step(challenge)
	if err != nil {
		return "", fmt.Errorf("step SCRAM conversation: %w", err)
	}

	return res, nil
}

func (x *XDGSCRAMClient) Done() bool {
	return x.ClientConversation.Done()
}
