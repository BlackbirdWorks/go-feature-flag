package s3publish_test

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/controlplane/s3publish"
)

// flagsBody is a valid flag file with one boolean flag; label makes the content differ.
func flagsBody(t *testing.T, label string) []byte {
	t.Helper()

	body, err := controlplane.MarshalFlags(controlplane.Flags{
		"flag-" + label: {
			Variations:  map[string]any{"on": true, "off": false},
			DefaultRule: &controlplane.Rule{Variation: new("off")},
		},
	})
	require.NoError(t, err)

	return body
}

// flagSetBody is a valid flag-set document with one flag set per (name, sources, key) triple.
func flagSetBody(t *testing.T, sets ...controlplane.FlagSet) []byte {
	t.Helper()

	body, err := controlplane.MarshalFlagSet(&controlplane.FlagSetDocument{FlagSets: sets})
	require.NoError(t, err)

	return body
}

func standardObjects(t *testing.T, label string) []s3publish.Object {
	t.Helper()

	return []s3publish.Object{
		{
			Name:   nameFlags,
			Kind:   controlplane.KindFlags,
			Format: controlplane.FormatYAML,
			Body:   flagsBody(t, label),
		},
		{
			Name: nameSets, Kind: controlplane.KindFlagSets, Format: controlplane.FormatYAML,
			Body: flagSetBody(t, controlplane.FlagSet{
				Name: "checkout", Sources: []string{nameFlags}, APIKeys: []string{"canary-api-key-0001"},
			}),
		},
	}
}

func testConfig() s3publish.Config {
	return s3publish.Config{Bucket: testBucket, Channel: testChannel, Connection: testConnection, Concurrency: 1}
}

func newPublisher(t *testing.T, api s3publish.API, mutate func(*s3publish.Config)) *s3publish.Publisher {
	t.Helper()

	cfg := testConfig()
	if mutate != nil {
		mutate(&cfg)
	}

	pub, err := s3publish.New(api, &cfg)
	require.NoError(t, err)

	return pub
}

// objectKey is the yaml key of body under the default prefix.
func objectKey(t *testing.T, body []byte) string {
	t.Helper()

	key, err := controlplane.ObjectKey(controlplane.ChecksumOf(body), controlplane.FormatYAML)
	require.NoError(t, err)

	return key
}

const (
	nameDryRun     = "dry run"
	nameNope       = "nope"
	nameRival      = "rival"
	nameOne        = "one"
	nameTwo        = "two"
	msgTooMany     = "too many objects"
	nameFlags      = "checkout-flags"
	nameSets       = "flagsets"
	testBucket     = "bkt"
	testChannel    = "stable"
	testConnection = "control"
	manifestKey    = "manifests/stable.yaml"
)

// newSHA returns the base64 sha256 of body, as the store reports it.
func newSHA(body []byte) *string {
	raw := sha256.Sum256(body)

	return aws.String(base64.StdEncoding.EncodeToString(raw[:]))
}
