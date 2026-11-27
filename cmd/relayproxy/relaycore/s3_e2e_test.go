//go:build docker

package relaycore_test

import (
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/relaycore"
	"github.com/blackbirdworks/semaphore/internal/connection"
	"github.com/blackbirdworks/semaphore/testhelpers/gopherstack"
)

var errStoreDown = errors.New("the store is down")

// outage is a round tripper that can make the emulator unreachable.
type outage struct {
	down atomic.Bool
}

func (o *outage) RoundTrip(req *http.Request) (*http.Response, error) {
	if o.down.Load() {
		return nil, errStoreDown
	}

	return http.DefaultTransport.RoundTrip(req)
}

// The scenario on a gopherstack bucket.
func TestE2E_S3Store(t *testing.T) {
	t.Parallel()

	srv := gopherstack.Connect(t)
	bucket := srv.CreateBucket(t, "relaycore")
	wire := &outage{}

	doc := "version: 2\nconnections:\n  " + connName + ":\n    type: s3\n    endpoint: " + srv.Endpoint +
		"\n    region: " + gopherstack.Region + "\n    bucket: " + bucket + "\n    pathStyle: true\n" +
		"    credentials:\n      type: static\n      accessKeyId: {fromEnv: AK}\n" +
		"      secretAccessKey: {fromEnv: SK}\n" + controlDoc(connName, "")

	runScenario(t, &storeFixture{
		put:  func(key string, data []byte) { srv.PutObject(t, bucket, key, data) },
		down: func() { wire.down.Store(true) },
		doc:  doc,
		env:  map[string]string{"AK": gopherstack.AccessKey, "SK": gopherstack.SecretKey},
	}, func(d *relaycore.Deps) {
		d.Connection = append(d.Connection, connection.WithRoundTripper(wire))
	})
}
