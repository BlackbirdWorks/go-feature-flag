//go:build docker

package corehttp_test

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	"github.com/blackbirdworks/semaphore/cmd/cli/segment"
	"github.com/blackbirdworks/semaphore/testhelpers/gopherstack"
)

// cliRig is a relay on a real Valkey, an S3 emulator with a bucket, and the bootstrap file of the command line: the
// relay's own (it holds the control plane and the store), plus the connection of the sources.
type cliRig struct {
	rig    *v2
	store  *valkeyStore
	srv    *gopherstack.Server
	config string
	bucket string
}

func newCLIRig(t *testing.T) *cliRig {
	t.Helper()

	srv, err := gopherstack.StartIsolated(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Terminate() })

	bucket := srv.CreateBucket(t, "ids")
	store := startValkey(t)
	rig := valkeyRig(t, store.address)

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "key"), []byte(gopherstack.AccessKey), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "secret"), []byte(gopherstack.SecretKey), 0o600))

	source := "  sources:\n    type: s3\n    region: " + gopherstack.Region + "\n    endpoint: " + srv.Endpoint +
		"\n    bucket: " + bucket + "\n    pathStyle: true\n    credentials:\n      type: static\n" +
		"      accessKeyId: {fromFile: " + filepath.Join(root, "key") + "}\n" +
		"      secretAccessKey: {fromFile: " + filepath.Join(root, "secret") + "}\n"

	config := filepath.Join(root, "bootstrap.yaml")
	doc := strings.Replace(rig.base, "controlPlane:", source+"controlPlane:", 1) + resolverSection
	require.NoError(t, os.WriteFile(config, []byte(doc), 0o600))

	return &cliRig{rig: rig, store: store, srv: srv, config: config, bucket: bucket}
}

// run runs the command line, with the segments resolved from the manifest of the relay.
func (env *cliRig) run(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer

	deps := segment.DefaultDeps()

	all := append(slices.Clone(args), "--config", env.config, "--segment", "audience")

	err := segment.Run(t.Context(), all, &helper.Streams{Stdout: &out, Stderr: &out}, &deps)

	return out.String(), err
}

func (env *cliRig) url(key string) string { return "s3://" + env.bucket + "/" + key }

// writes counts the commands of the store that import: SADD, the scripts and SET.
func (s *valkeyStore) writes(tb testing.TB) int {
	tb.Helper()

	info, err := s.client.Do(tb.Context(), s.client.B().Info().Section("commandstats").Build()).ToString()
	require.NoError(tb, err)

	total := 0

	for line := range strings.SplitSeq(info, "\n") {
		name, stats, found := strings.Cut(strings.TrimSpace(line), ":")
		if !found || !strings.HasPrefix(stats, "calls=") {
			continue
		}

		switch name {
		case "cmdstat_sadd", "cmdstat_eval", "cmdstat_evalsha", "cmdstat_set", "cmdstat_unlink":
			calls, _, _ := strings.Cut(strings.TrimPrefix(stats, "calls="), ",")
			count, convErr := strconv.Atoi(calls)
			require.NoError(tb, convErr)

			total += count
		}
	}

	return total
}

// An import from an object of the emulator, with the segment resolved from the published manifest, is what the relay
// answers from: the members of the object are targeted and others are not. The sync of an unchanged object writes
// nothing to the store, a replaced object is imported by the next run, and a prefix of objects is one input.
func TestE2E_CLIImportAndSyncFromS3(t *testing.T) {
	t.Parallel()

	env := newCLIRig(t)

	env.srv.PutObject(t, env.bucket, "ids/list.txt", []byte("alice\nbob\n"))

	out, err := env.run(t, "import", "--from", env.url("ids/list.txt"))
	require.NoError(t, err, out)
	assert.Contains(t, out, "swapped: true")

	awaitEval(t, env.rig, "alice", isMember, "a member of the imported object is targeted")
	awaitEval(t, env.rig, "carol", func(b evalBody) bool { return !b.Value }, "another id is not")

	before := env.store.writes(t)

	out, err = env.run(t, "sync", "--once", "--from", env.url("ids/list.txt"))
	require.NoError(t, err, out)
	assert.Contains(t, out, "outcome=unchanged")
	assert.Equal(t, before, env.store.writes(t), "an unchanged source writes nothing to the store")

	env.srv.PutObject(t, env.bucket, "ids/list.txt", []byte("alice\ncarol\n"))

	out, err = env.run(t, "sync", "--once", "--from", env.url("ids/list.txt"))
	require.NoError(t, err, out)
	assert.Contains(t, out, "outcome=changed")

	awaitEval(t, env.rig, "carol", isMember, "the replaced object is imported by the next run")
	awaitEval(t, env.rig, "bob", func(b evalBody) bool { return !b.Value }, "a member that left the object is gone")

	before = env.store.writes(t)

	out, err = env.run(t, "sync", "--once", "--from", env.url("ids/list.txt"))
	require.NoError(t, err, out)
	assert.Contains(t, out, "outcome=unchanged")
	assert.Equal(t, before, env.store.writes(t), "the second run of an unchanged source writes nothing")

	env.srv.PutObject(t, env.bucket, "parts/one.txt", []byte("dave\n"))
	env.srv.PutObject(t, env.bucket, "parts/two.txt.gz", gzipText(t, "erin\nfrank"))
	env.srv.PutObject(t, env.bucket, "parts/three.txt", []byte("grace\n"))

	out, err = env.run(t, "import", "--from", env.url("parts/"))
	require.NoError(t, err, out)
	assert.Contains(t, out, "added: 4")

	for _, member := range []string{"dave", "erin", "frank", "grace"} {
		awaitEval(t, env.rig, member, isMember, "a member of a prefix of objects is targeted")
	}

	awaitEval(t, env.rig, "alice", func(b evalBody) bool { return !b.Value }, "the old set is replaced")
}
