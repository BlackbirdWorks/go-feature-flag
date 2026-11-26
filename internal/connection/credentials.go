package connection

import (
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
)

// redactedProvider is the credentials provider placed in the [aws.Config] the factory returns. It holds the
// real provider behind closures, so printing the configuration (or a client built from it) never shows key
// material: fmt prints a function as an address, and this type formats as [RedactedValue].
type redactedProvider struct {
	retrieve   func(context.Context) (aws.Credentials, error)
	invalidate func()
}

// newRedacted wraps provider. The credentials cache of the SDK, when there is one, stays the single cache
// that owns the credentials: [redactedProvider.Invalidate] forwards to it.
func newRedacted(provider aws.CredentialsProvider) redactedProvider {
	red := redactedProvider{retrieve: provider.Retrieve, invalidate: func() {}}

	if inv, ok := provider.(interface{ Invalidate() }); ok {
		red.invalidate = inv.Invalidate
	}

	return red
}

// Retrieve implements [aws.CredentialsProvider].
func (p redactedProvider) Retrieve(ctx context.Context) (aws.Credentials, error) {
	return p.retrieve(ctx)
}

// Invalidate forwards to the cache that owns the credentials, so the next Retrieve refreshes them.
func (p redactedProvider) Invalidate() { p.invalidate() }

// Format implements [fmt.Formatter]: every verb prints [RedactedValue].
func (redactedProvider) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, RedactedValue) }

// String implements [fmt.Stringer].
func (redactedProvider) String() string { return RedactedValue }

// GoString implements [fmt.GoStringer].
func (redactedProvider) GoString() string { return RedactedValue }

// staticCredentials returns the provider of a static key. The keys stay in the closure.
func staticCredentials(creds *Credentials) redactedProvider {
	keyID, secret, token := creds.AccessKeyID, creds.SecretAccessKey, creds.SessionToken

	return redactedProvider{
		retrieve: func(context.Context) (aws.Credentials, error) {
			return aws.Credentials{
				AccessKeyID:     keyID.Reveal(),
				SecretAccessKey: secret.Reveal(),
				SessionToken:    token.Reveal(),
				Source:          "semaphore connection static credentials",
			}, nil
		},
		invalidate: func() {},
	}
}
