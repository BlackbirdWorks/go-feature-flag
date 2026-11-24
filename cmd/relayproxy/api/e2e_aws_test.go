//go:build docker

package api_test

// These tests run a real relay proxy (config file -> flagset manager -> API server) against the
// gopherstack AWS emulator, started once for the package by testutils/gopherstack.
//
// The relay proxy builds its AWS clients with config.LoadDefaultConfig and has no endpoint
// setting, so the only way to point it to the emulator is the AWS_* environment variables. The
// top-level tests therefore use t.Setenv (through Server.SetAwsEnv) and can NOT call t.Parallel():
// this is an accepted exception. Their subtests do not touch the environment and run in parallel.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	kinesistypes "github.com/aws/aws-sdk-go-v2/service/kinesis/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/testutils/gopherstack"
)

// e2eFlags returns the flag file of a flag set. The same flag keys are used by every flag set with
// values prefixed by tag, so a response proves which flag set served it.
func e2eFlags(tag, ruleOnValue string, extraFlags ...string) []byte {
	s := fmt.Sprintf(`
rule-flag:
  variations:
    on: %[2]s
    off: %[1]s-off
  targeting:
    - name: admins
      query: group eq "admin"
      variation: on
  defaultRule:
    variation: off
pct-flag:
  variations:
    a: %[1]s-A
    b: %[1]s-B
  defaultRule:
    percentage:
      a: 100
      b: 0
split-flag:
  variations:
    a: %[1]s-A
    b: %[1]s-B
  defaultRule:
    percentage:
      a: 50
      b: 50
static-flag:
  variations:
    t: true
    f: false
  defaultRule:
    variation: f
only-in-%[1]s:
  variations:
    v: %[1]s-only
  defaultRule:
    variation: v
`, tag, ruleOnValue)

	return []byte(s + strings.Join(extraFlags, "\n"))
}

func ofrepCtx(key string, kv ...any) map[string]any {
	c := map[string]any{"targetingKey": key}
	for i := 0; i+1 < len(kv); i += 2 {
		c[kv[i].(string)] = kv[i+1]
	}

	return map[string]any{"context": c}
}

func goffCtx(key string, kv ...any) map[string]any {
	custom := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		custom[kv[i].(string)] = kv[i+1]
	}

	return map[string]any{
		"evaluationContext": map[string]any{"key": key, "custom": custom},
		"defaultValue":      "sdk-default",
	}
}

const flagSetsYAML = `
server:
  port: {{ .Port }}
  mode: http
hideBanner: true
pollingInterval: 1000
flagsets:
  - name: team-a
    apiKeys: [key-a]
    pollingInterval: 1000
    enableSharedPollerPerBucket: {{ .Shared }}
    retriever:
      kind: s3
      bucket: {{ .Bucket }}
      item: flags/a.yaml
  - name: team-b
    apiKeys: [key-b1, key-b2]
    pollingInterval: 1000
    enableSharedPollerPerBucket: {{ .Shared }}
    retriever:
      kind: s3
      bucket: {{ .Bucket }}
      item: flags/b.yaml
`

func TestE2E_RelayProxy_S3FlagSets(t *testing.T) {
	t.Parallel()

	// no t.Parallel(): t.Setenv, see the comment at the top of the file.
	srv := gopherstack.Shared(t)
	srv.SetAwsEnv(t)

	modes := []struct {
		name   string
		shared bool
	}{
		{name: "one retriever per flag set", shared: false},
		{name: "shared poller per bucket", shared: true},
	}
	for _, mode := range modes {
		t.Run(mode.name, func(t *testing.T) {
			t.Parallel()

			bucket := srv.CreateBucket(t, "rp")
			srv.PutObject(t, bucket, "flags/a.yaml", e2eFlags("A", "A-on"))
			srv.PutObject(t, bucket, "flags/b.yaml", e2eFlags("B", "B-on"))
			url := startRelayProxyFromYAML(t, flagSetsYAML, map[string]any{"Bucket": bucket, "Shared": mode.shared})

			t.Run("single evaluation", func(t *testing.T) {
				t.Parallel()

				tests := []struct {
					name        string
					path        string
					apiKey      string
					body        map[string]any
					wantStatus  int
					wantValue   any
					wantVariant string
					wantReason  string
					wantErrCode string
				}{
					// OFREP
					{
						name:        "ofrep targeting rule match",
						path:        "/ofrep/v1/evaluate/flags/rule-flag",
						apiKey:      "key-a",
						body:        ofrepCtx("u1", "group", "admin"),
						wantStatus:  200,
						wantValue:   "A-on",
						wantVariant: "on",
						wantReason:  "TARGETING_MATCH",
					},
					{
						name:        "ofrep targeting rule no match falls to default rule",
						path:        "/ofrep/v1/evaluate/flags/rule-flag",
						apiKey:      "key-a",
						body:        ofrepCtx("u1", "group", "user"),
						wantStatus:  200,
						wantValue:   "A-off",
						wantVariant: "off",
						wantReason:  "DEFAULT",
					},
					{
						name:        "ofrep other flag set serves its own value",
						path:        "/ofrep/v1/evaluate/flags/rule-flag",
						apiKey:      "key-b1",
						body:        ofrepCtx("u1", "group", "admin"),
						wantStatus:  200,
						wantValue:   "B-on",
						wantVariant: "on",
						wantReason:  "TARGETING_MATCH",
					},
					{
						name:        "ofrep second api key of the flag set",
						path:        "/ofrep/v1/evaluate/flags/rule-flag",
						apiKey:      "key-b2",
						body:        ofrepCtx("u1", "group", "admin"),
						wantStatus:  200,
						wantValue:   "B-on",
						wantVariant: "on",
						wantReason:  "TARGETING_MATCH",
					},
					{
						name:        "ofrep static percentage",
						path:        "/ofrep/v1/evaluate/flags/pct-flag",
						apiKey:      "key-a",
						body:        ofrepCtx("u1"),
						wantStatus:  200,
						wantValue:   "A-A",
						wantVariant: "a",
						wantReason:  "STATIC",
					},
					{
						name:        "ofrep split user-1",
						path:        "/ofrep/v1/evaluate/flags/split-flag",
						apiKey:      "key-a",
						body:        ofrepCtx("user-1"),
						wantStatus:  200,
						wantValue:   "A-A",
						wantVariant: "a",
						wantReason:  "SPLIT",
					},
					{
						name:        "ofrep split user-0",
						path:        "/ofrep/v1/evaluate/flags/split-flag",
						apiKey:      "key-a",
						body:        ofrepCtx("user-0"),
						wantStatus:  200,
						wantValue:   "A-B",
						wantVariant: "b",
						wantReason:  "SPLIT",
					},
					{
						name:        "ofrep static boolean",
						path:        "/ofrep/v1/evaluate/flags/static-flag",
						apiKey:      "key-b1",
						body:        ofrepCtx("u1"),
						wantStatus:  200,
						wantValue:   false,
						wantVariant: "f",
						wantReason:  "STATIC",
					},
					{
						name:        "ofrep flag of the other flag set is not found",
						path:        "/ofrep/v1/evaluate/flags/only-in-B",
						apiKey:      "key-a",
						body:        ofrepCtx("u1"),
						wantStatus:  404,
						wantErrCode: "FLAG_NOT_FOUND",
					},
					{
						name:        "ofrep flag only in own flag set",
						path:        "/ofrep/v1/evaluate/flags/only-in-B",
						apiKey:      "key-b1",
						body:        ofrepCtx("u1"),
						wantStatus:  200,
						wantValue:   "B-only",
						wantVariant: "v",
						wantReason:  "STATIC",
					},
					{
						name:        "ofrep unknown flag",
						path:        "/ofrep/v1/evaluate/flags/nope",
						apiKey:      "key-a",
						body:        ofrepCtx("u1"),
						wantStatus:  404,
						wantErrCode: "FLAG_NOT_FOUND",
					},
					{
						name:        "ofrep missing context",
						path:        "/ofrep/v1/evaluate/flags/rule-flag",
						apiKey:      "key-a",
						body:        map[string]any{},
						wantStatus:  400,
						wantErrCode: "INVALID_CONTEXT",
					},
					{
						name:       "ofrep wrong api key",
						path:       "/ofrep/v1/evaluate/flags/rule-flag",
						apiKey:     "wrong",
						body:       ofrepCtx("u1"),
						wantStatus: 401,
					},
					{
						name:       "ofrep no api key",
						path:       "/ofrep/v1/evaluate/flags/rule-flag",
						apiKey:     "",
						body:       ofrepCtx("u1"),
						wantStatus: 401,
					},
					// GOFF
					{
						name:        "goff targeting rule match",
						path:        "/v1/feature/rule-flag/eval",
						apiKey:      "key-a",
						body:        goffCtx("u1", "group", "admin"),
						wantStatus:  200,
						wantValue:   "A-on",
						wantVariant: "on",
						wantReason:  "TARGETING_MATCH",
					},
					{
						name:        "goff default rule",
						path:        "/v1/feature/rule-flag/eval",
						apiKey:      "key-b1",
						body:        goffCtx("u1", "group", "user"),
						wantStatus:  200,
						wantValue:   "B-off",
						wantVariant: "off",
						wantReason:  "DEFAULT",
					},
					{
						name:        "goff split user-0",
						path:        "/v1/feature/split-flag/eval",
						apiKey:      "key-b2",
						body:        goffCtx("user-0"),
						wantStatus:  200,
						wantValue:   "B-B",
						wantVariant: "b",
						wantReason:  "SPLIT",
					},
					{
						name:        "goff static percentage",
						path:        "/v1/feature/pct-flag/eval",
						apiKey:      "key-a",
						body:        goffCtx("u1"),
						wantStatus:  200,
						wantValue:   "A-A",
						wantVariant: "a",
						wantReason:  "STATIC",
					},
					{
						name:        "goff unknown flag returns the default value",
						path:        "/v1/feature/nope/eval",
						apiKey:      "key-a",
						body:        goffCtx("u1"),
						wantStatus:  200,
						wantValue:   "sdk-default",
						wantVariant: "SdkDefault",
						wantReason:  "ERROR",
						wantErrCode: "FLAG_NOT_FOUND",
					},
					{
						name:       "goff wrong api key",
						path:       "/v1/feature/rule-flag/eval",
						apiKey:     "wrong",
						body:       goffCtx("u1"),
						wantStatus: 401,
					},
				}
				for _, tt := range tests {
					t.Run(tt.name, func(t *testing.T) {
						t.Parallel()
						res := postJSON(t, url+tt.path, tt.apiKey, tt.body)
						require.Equal(t, tt.wantStatus, res.Status, res.Raw)
						if tt.wantStatus == 401 {
							return
						}
						isOFREP := strings.HasPrefix(tt.path, "/ofrep/")
						if tt.wantErrCode != "" {
							assert.Equal(t, tt.wantErrCode, res.Body["errorCode"], res.Raw)
							if tt.wantStatus != 200 {
								return
							}
						}
						assert.Equal(t, tt.wantValue, res.Body["value"], res.Raw)
						assert.Equal(t, tt.wantReason, res.Body["reason"], res.Raw)
						if isOFREP {
							assert.Equal(t, tt.wantVariant, res.Body["variant"], res.Raw)
						} else {
							assert.Equal(t, tt.wantVariant, res.Body["variationType"], res.Raw)
						}
					})
				}
			})

			t.Run("bulk evaluation", func(t *testing.T) {
				t.Parallel()

				tests := []struct {
					name       string
					path       string
					apiKey     string
					body       map[string]any
					wantStatus int
					wantFlags  map[string]any // flag key -> value
				}{
					{
						name:       "ofrep bulk flag set a",
						path:       "/ofrep/v1/evaluate/flags",
						apiKey:     "key-a",
						body:       ofrepCtx("user-1", "group", "admin"),
						wantStatus: 200,
						wantFlags: map[string]any{
							"rule-flag":   "A-on",
							"pct-flag":    "A-A",
							"split-flag":  "A-A",
							"static-flag": false,
							"only-in-A":   "A-only",
						},
					},
					{
						name:       "ofrep bulk flag set b",
						path:       "/ofrep/v1/evaluate/flags",
						apiKey:     "key-b2",
						body:       ofrepCtx("user-0", "group", "user"),
						wantStatus: 200,
						wantFlags: map[string]any{
							"rule-flag":   "B-off",
							"pct-flag":    "B-A",
							"split-flag":  "B-B",
							"static-flag": false,
							"only-in-B":   "B-only",
						},
					},
					{
						name:       "ofrep bulk wrong api key",
						path:       "/ofrep/v1/evaluate/flags",
						apiKey:     "wrong",
						body:       ofrepCtx("u"),
						wantStatus: 401,
					},
					{
						name:       "goff allflags flag set a",
						path:       "/v1/allflags",
						apiKey:     "key-a",
						body:       goffCtx("user-1", "group", "admin"),
						wantStatus: 200,
						wantFlags: map[string]any{
							"rule-flag":   "A-on",
							"pct-flag":    "A-A",
							"split-flag":  "A-A",
							"static-flag": false,
							"only-in-A":   "A-only",
						},
					},
					{
						name:       "goff allflags flag set b",
						path:       "/v1/allflags",
						apiKey:     "key-b1",
						body:       goffCtx("user-0", "group", "user"),
						wantStatus: 200,
						wantFlags: map[string]any{
							"rule-flag":   "B-off",
							"pct-flag":    "B-A",
							"split-flag":  "B-B",
							"static-flag": false,
							"only-in-B":   "B-only",
						},
					},
					{
						name:       "goff allflags wrong api key",
						path:       "/v1/allflags",
						apiKey:     "wrong",
						body:       goffCtx("u"),
						wantStatus: 401,
					},
				}
				for _, tt := range tests {
					t.Run(tt.name, func(t *testing.T) {
						t.Parallel()
						res := postJSON(t, url+tt.path, tt.apiKey, tt.body)
						require.Equal(t, tt.wantStatus, res.Status, res.Raw)
						if tt.wantStatus != 200 {
							return
						}
						got := map[string]any{}
						if flags, ok := res.Body["flags"].([]any); ok { // OFREP: a list
							for _, f := range flags {
								m := f.(map[string]any)
								got[m["key"].(string)] = m["value"]
							}
						} else if flags, ok := res.Body["flags"].(map[string]any); ok { // GOFF: a map
							for k, f := range flags {
								got[k] = f.(map[string]any)["value"]
							}
						}
						assert.Equal(t, tt.wantFlags, got, res.Raw)
					})
				}
			})

			// Runs once the parallel evaluations above are over (a group waits for its parallel children).
			t.Run("update in S3 is served after the polling interval", func(t *testing.T) {
				t.Parallel()

				srv.PutObject(t, bucket, "flags/a.yaml", e2eFlags("A", "A-on-v2"))
				require.Eventually(t, func() bool {
					res := postJSON(
						t,
						url+"/ofrep/v1/evaluate/flags/rule-flag",
						"key-a",
						ofrepCtx("u1", "group", "admin"),
					)

					return res.Status == 200 && res.Body["value"] == "A-on-v2"
				}, 30*time.Second, 200*time.Millisecond, "new value of flag set a never served")
				// GOFF sees it too, and the other flag set is untouched.
				res := postJSON(t, url+"/v1/feature/rule-flag/eval", "key-a", goffCtx("u1", "group", "admin"))
				assert.Equal(t, "A-on-v2", res.Body["value"], res.Raw)
				res = postJSON(t, url+"/ofrep/v1/evaluate/flags/rule-flag", "key-b1", ofrepCtx("u1", "group", "admin"))
				assert.Equal(t, "B-on", res.Body["value"], res.Raw)

				// a flag removed from the file disappears, a flag added appears
				srv.PutObject(t, bucket, "flags/b.yaml", e2eFlags("B", "B-on", `
brand-new:
  variations:
    v: new
  defaultRule:
    variation: v
`))
				require.Eventually(t, func() bool {
					res := postJSON(t, url+"/ofrep/v1/evaluate/flags/brand-new", "key-b1", ofrepCtx("u1"))

					return res.Status == 200 && res.Body["value"] == "new"
				}, 30*time.Second, 200*time.Millisecond, "new flag of flag set b never served")
			})
		})
	}
}

func TestE2E_RelayProxy_TopLevelS3Retriever(t *testing.T) {
	t.Parallel()

	// no t.Parallel(): t.Setenv, see the comment at the top of the file.
	srv := gopherstack.Shared(t)
	srv.SetAwsEnv(t)

	tests := []struct {
		name      string
		retriever string // YAML of the retriever, {{ .Bucket }} is replaced
		objects   map[string]string
		wantValue string
	}{
		{
			name: "single item",
			retriever: `
retriever:
  kind: s3
  bucket: {{ .Bucket }}
  item: flags.yaml`,
			objects:   map[string]string{"flags.yaml": "A"},
			wantValue: "A-on",
		},
		{
			name: "folder with pattern, flags split in several files",
			retriever: `
retriever:
  kind: s3
  bucket: {{ .Bucket }}
  prefix: flags/
  pattern: "*.yaml"
  recursive: true`,
			objects: map[string]string{
				"flags/one.yaml":     "A",
				"flags/sub/two.yaml": "B",
				"flags/ignored.txt":  "garbage: [",
			},
			wantValue: "A-on",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bucket := srv.CreateBucket(t, "top")
			for k, tag := range tt.objects {
				switch tag {
				case "A":
					srv.PutObject(t, bucket, k, e2eFlags("A", "A-on"))
				case "B":
					srv.PutObject(
						t,
						bucket,
						k,
						[]byte("only-in-B:\n  variations:\n    v: B-only\n  defaultRule:\n    variation: v\n"),
					)
				default:
					srv.PutObject(t, bucket, k, []byte(tag))
				}
			}
			yaml := strings.ReplaceAll(`
server:
  port: {{ .Port }}
  mode: http
hideBanner: true
pollingInterval: 1000
authorizedKeys:
  evaluation: [top-key]
`+tt.retriever+"\n", "{{ .Bucket }}", bucket)
			url := startRelayProxyFromYAML(t, yaml, nil)

			res := postJSON(t, url+"/ofrep/v1/evaluate/flags/rule-flag", "top-key", ofrepCtx("u1", "group", "admin"))
			require.Equal(t, 200, res.Status, res.Raw)
			assert.Equal(t, tt.wantValue, res.Body["value"])
			assert.Equal(t, "TARGETING_MATCH", res.Body["reason"])
			if strings.Contains(tt.name, "folder") {
				res = postJSON(t, url+"/ofrep/v1/evaluate/flags/only-in-B", "top-key", ofrepCtx("u1"))
				require.Equal(t, 200, res.Status, res.Raw)
				assert.Equal(t, "B-only", res.Body["value"])
			}
			assert.Equal(t, 401, postJSON(t, url+"/ofrep/v1/evaluate/flags/rule-flag", "nope", ofrepCtx("u1")).Status)

			// update
			first := ""
			for k, tag := range tt.objects {
				if tag == "A" {
					first = k
				}
			}
			srv.PutObject(t, bucket, first, e2eFlags("A", "A-on-v2"))
			require.Eventually(t, func() bool {
				r := postJSON(t, url+"/v1/feature/rule-flag/eval", "top-key", goffCtx("u1", "group", "admin"))

				return r.Status == 200 && r.Body["value"] == "A-on-v2"
			}, 30*time.Second, 200*time.Millisecond)
		})
	}
}

// ---------------------------------------------------------------------------------------------
// exporters

const exportersYAML = `
server:
  port: {{ .Port }}
  mode: http
hideBanner: true
pollingInterval: 1000
flagsets:
  - name: team-a
    apiKeys: [key-a]
    pollingInterval: 1000
    retriever:
      kind: s3
      bucket: {{ .Bucket }}
      item: flags/a.yaml
    exporters:
      - kind: s3
        bucket: {{ .Bucket }}
        path: exports/a
        format: json
        flushInterval: 1000
        maxEventInMemory: 100
      - kind: kinesis
        streamName: {{ .Stream }}
        flushInterval: 1000
        maxEventInMemory: 100
      - kind: sqs
        queueUrl: {{ .QueueURL }}
        flushInterval: 1000
        maxEventInMemory: 100
`

type sinkEvent struct {
	Key, UserKey, Variation string
	Value                   any
}

func TestE2E_RelayProxy_ExportersToAWS(t *testing.T) {
	t.Parallel()

	// no t.Parallel(): t.Setenv, see the comment at the top of the file.
	srv := gopherstack.Shared(t)
	srv.SetAwsEnv(t)

	bucket := srv.CreateBucket(t, "exp")
	srv.PutObject(t, bucket, "flags/a.yaml", e2eFlags("A", "A-on"))

	cfg := srv.MustAwsConfig(t)
	kc := kinesis.NewFromConfig(cfg)
	stream := gopherstack.UniqueName(t, "stream")
	_, err := kc.CreateStream(
		t.Context(),
		&kinesis.CreateStreamInput{StreamName: aws.String(stream), ShardCount: aws.Int32(1)},
	)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		out, err := kc.DescribeStream(t.Context(), &kinesis.DescribeStreamInput{StreamName: aws.String(stream)})

		return err == nil && out.StreamDescription.StreamStatus == kinesistypes.StreamStatusActive
	}, 30*time.Second, 100*time.Millisecond)
	sc := sqs.NewFromConfig(cfg)
	q, err := sc.CreateQueue(
		t.Context(),
		&sqs.CreateQueueInput{QueueName: aws.String(gopherstack.UniqueName(t, "queue"))},
	)
	require.NoError(t, err)

	url := startRelayProxyFromYAML(t, exportersYAML, map[string]any{
		"Bucket": bucket, "Stream": stream, "QueueURL": aws.ToString(q.QueueUrl),
	})

	// evaluations from the three entry points, each with its own user
	wantUsers := map[string]string{} // user key -> expected value of rule-flag
	for i := range 4 {
		u := fmt.Sprintf("ofrep-%d", i)
		wantUsers[u] = "A-on"
		require.Equal(
			t,
			200,
			postJSON(t, url+"/ofrep/v1/evaluate/flags/rule-flag", "key-a", ofrepCtx(u, "group", "admin")).Status,
		)
	}
	for i := range 3 {
		u := fmt.Sprintf("goff-%d", i)
		wantUsers[u] = "A-off"
		require.Equal(
			t,
			200,
			postJSON(t, url+"/v1/feature/rule-flag/eval", "key-a", goffCtx(u, "group", "user")).Status,
		)
	}

	// assertSink checks that the events read from a sink contain every evaluation made above.
	assertSink := func(t *testing.T, read func(t *testing.T) []sinkEvent) {
		var got []sinkEvent
		require.Eventually(t, func() bool {
			got = read(t)
			users := map[string]bool{}
			for _, e := range got {
				if e.Key == "rule-flag" {
					users[e.UserKey] = true
				}
			}

			return len(users) >= len(wantUsers)
		}, 30*time.Second, 300*time.Millisecond, "events received: %d", len(got))
		seen := map[string]sinkEvent{}
		for _, e := range got {
			if e.Key == "rule-flag" {
				seen[e.UserKey] = e
			}
		}
		for u, v := range wantUsers {
			e, ok := seen[u]
			if assert.True(t, ok, "no event for user %s", u) {
				assert.Equal(t, v, e.Value, "user %s", u)
				assert.Equal(t, map[string]string{"A-on": "on", "A-off": "off"}[v], e.Variation, "user %s", u)
			}
		}
	}

	sqsReceived := map[string]sinkEvent{} // only used by the sqs subtest
	sinks := []struct {
		name string
		read func(t *testing.T) []sinkEvent
	}{
		{
			name: "s3 exporter",
			read: func(t *testing.T) []sinkEvent {
				var res []sinkEvent
				for _, k := range srv.ListKeys(t, bucket, "exports/a/") {
					require.True(t, strings.HasSuffix(k, ".json"), k)
					for line := range strings.SplitSeq(strings.TrimSpace(string(srv.GetObject(t, bucket, k))), "\n") {
						res = append(res, decodeFeatureEvent(t, []byte(line)))
					}
				}

				return res
			},
		},
		{
			name: "kinesis exporter",
			read: func(t *testing.T) []sinkEvent {
				desc, err := kc.DescribeStream(
					t.Context(),
					&kinesis.DescribeStreamInput{StreamName: aws.String(stream)},
				)
				require.NoError(t, err)
				var res []sinkEvent
				for _, s := range desc.StreamDescription.Shards {
					it, err := kc.GetShardIterator(t.Context(), &kinesis.GetShardIteratorInput{
						StreamName: aws.String(
							stream,
						),
						ShardId:           s.ShardId,
						ShardIteratorType: kinesistypes.ShardIteratorTypeTrimHorizon,
					})
					require.NoError(t, err)
					out, err := kc.GetRecords(
						t.Context(),
						&kinesis.GetRecordsInput{ShardIterator: it.ShardIterator, Limit: aws.Int32(1000)},
					)
					require.NoError(t, err)
					for _, r := range out.Records {
						res = append(res, decodeFeatureEvent(t, r.Data))
					}
				}

				return res
			},
		},
		{
			name: "sqs exporter",
			read: func(t *testing.T) []sinkEvent {
				return receiveSQS(t, sc, aws.ToString(q.QueueUrl), sqsReceived)
			},
		},
	}
	for _, s := range sinks {
		t.Run(s.name, func(t *testing.T) {
			t.Parallel()
			assertSink(t, s.read)
		})
	}
}

// receiveSQS receives and deletes the messages available, and adds them to acc (message id -> event),
// so the successive polls of an Eventually accumulate everything received.
func receiveSQS(t *testing.T, sc *sqs.Client, queueURL string, acc map[string]sinkEvent) []sinkEvent {
	t.Helper()
	out, err := sc.ReceiveMessage(t.Context(), &sqs.ReceiveMessageInput{
		QueueUrl: aws.String(queueURL), MaxNumberOfMessages: 10, WaitTimeSeconds: 1,
	})
	require.NoError(t, err)
	for _, m := range out.Messages {
		e := decodeFeatureEvent(t, []byte(aws.ToString(m.Body)))
		acc[aws.ToString(m.MessageId)] = e
		_, err := sc.DeleteMessage(
			t.Context(),
			&sqs.DeleteMessageInput{QueueUrl: aws.String(queueURL), ReceiptHandle: m.ReceiptHandle},
		)
		require.NoError(t, err)
	}
	res := make([]sinkEvent, 0, len(acc))
	for _, e := range acc {
		res = append(res, e)
	}
	sort.Slice(res, func(i, j int) bool { return res[i].UserKey < res[j].UserKey })

	return res
}

func decodeFeatureEvent(t *testing.T, b []byte) sinkEvent {
	t.Helper()
	var e exporter.FeatureEvent
	require.NoError(t, json.Unmarshal(b, &e), string(b))

	return sinkEvent{Key: e.Key, UserKey: e.UserKey, Variation: e.Variation, Value: e.Value}
}
