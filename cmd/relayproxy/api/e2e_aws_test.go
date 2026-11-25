//go:build docker

package api_test

// These tests run a real relay proxy (config file -> flagset manager -> API server) against the
// gopherstack AWS emulator, started once for the package by testhelpers/gopherstack.
//
// The relay proxy builds its AWS clients with config.LoadDefaultConfig and has no endpoint
// setting, so the only way to point it to the emulator is the AWS_* environment variables. The
// top-level tests therefore use t.Setenv (through Server.SetAwsEnv) and can NOT call t.Parallel():
// this is an accepted exception. Their subtests do not touch the environment and run in parallel.

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
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
	"github.com/blackbirdworks/semaphore/testhelpers/gopherstack"
)

const (
	upperAName       = "A-A"
	aOff             = "A-off"
	aOn              = "A-on"
	aOnV2            = "A-on-v2"
	upperBName       = "B-B"
	bOff             = "B-off"
	bOn              = "B-on"
	bOnly            = "B-only"
	flagNotFoundCode = "FLAG_NOT_FOUND"
	flagsAYaml       = "flags/a.yaml"
	group            = "group"
	keyA             = "key-a"
	keyB1            = "key-b1"
	keyB2            = "key-b2"
	off              = "off"
	ofrepV1Evaluate3 = "/ofrep/v1/evaluate/flags/only-in-B"
	ofrepV1Evaluate4 = "/ofrep/v1/evaluate/flags/rule-flag"
	pctFlag          = "pct-flag"
	ruleFlag         = "rule-flag"
	sPLIT            = "SPLIT"
	sTATIC           = "STATIC"
	splitFlag        = "split-flag"
	staticFlag       = "static-flag"
	tARGETINGMATCH   = "TARGETING_MATCH"
	topKey           = "top-key"
	user             = "user"
	user0            = "user-0"
	user1            = "user-1"
	v1FeatureRule    = "/v1/feature/rule-flag/eval"
	value2           = "value"
	wrong            = "wrong"
)

// e2eFlags returns the flag file of a flag set. The same flag keys are used by every flag set with
// values prefixed by tag, so a response proves which flag set served it.
func e2eFlags(tag, ruleOnValue string, extraFlags ...string) []byte {
	flagsYAML := fmt.Sprintf(`
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

	return []byte(flagsYAML + strings.Join(extraFlags, "\n"))
}

func ofrepCtx(key string, kv ...any) map[string]any {
	ctxMap := map[string]any{"targetingKey": key}

	for i := 0; i+1 < len(kv); i += 2 {
		if k, ok := kv[i].(string); ok {
			ctxMap[k] = kv[i+1]
		}
	}

	return map[string]any{"context": ctxMap}
}

func goffCtx(key string, kv ...any) map[string]any {
	custom := map[string]any{}

	for i := 0; i+1 < len(kv); i += 2 {
		if k, ok := kv[i].(string); ok {
			custom[k] = kv[i+1]
		}
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

//nolint:paralleltest // uses t.Setenv so the AWS SDK default config chain reads test endpoint/credentials
func TestE2E_RelayProxy_S3FlagSets(t *testing.T) {
	// no t.Parallel(): t.Setenv, see the comment at the top of the file.
	srv := gopherstack.Connect(t)
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
			bucket := srv.CreateBucket(t, "rp")
			srv.PutObject(t, bucket, flagsAYaml, e2eFlags("A", aOn))
			srv.PutObject(t, bucket, "flags/b.yaml", e2eFlags("B", bOn))
			url := startRelayProxyFromYAML(t, flagSetsYAML, map[string]any{"Bucket": bucket, "Shared": mode.shared})

			t.Run("single evaluation", func(t *testing.T) {
				runS3FlagSetsSingleEval(t, url)
			})
			t.Run("bulk evaluation", func(t *testing.T) {
				runS3FlagSetsBulkEval(t, url)
			})
			// Runs once the parallel evaluations above are over (a group waits for its parallel children).
			t.Run("update in S3 is served after the polling interval", func(t *testing.T) {
				verifyS3FlagSetsReload(t, srv, bucket, url)
			})
		})
	}
}

func runS3FlagSetsSingleEval(t *testing.T, url string) {
	t.Helper()

	tests := []struct {
		wantValue   any
		body        map[string]any
		name        string
		path        string
		apiKey      string
		wantVariant string
		wantReason  string
		wantErrCode string
		wantStatus  int
	}{
		// OFREP
		{
			name:        "ofrep targeting rule match",
			path:        ofrepV1Evaluate4,
			apiKey:      keyA,
			body:        ofrepCtx("u1", group, admin),
			wantStatus:  200,
			wantValue:   aOn,
			wantVariant: "on",
			wantReason:  tARGETINGMATCH,
		},
		{
			name:        "ofrep targeting rule no match falls to default rule",
			path:        ofrepV1Evaluate4,
			apiKey:      keyA,
			body:        ofrepCtx("u1", group, user),
			wantStatus:  200,
			wantValue:   aOff,
			wantVariant: off,
			wantReason:  "DEFAULT",
		},
		{
			name:        "ofrep other flag set serves its own value",
			path:        ofrepV1Evaluate4,
			apiKey:      keyB1,
			body:        ofrepCtx("u1", group, admin),
			wantStatus:  200,
			wantValue:   bOn,
			wantVariant: "on",
			wantReason:  tARGETINGMATCH,
		},
		{
			name:        "ofrep second api key of the flag set",
			path:        ofrepV1Evaluate4,
			apiKey:      keyB2,
			body:        ofrepCtx("u1", group, admin),
			wantStatus:  200,
			wantValue:   bOn,
			wantVariant: "on",
			wantReason:  tARGETINGMATCH,
		},
		{
			name:        "ofrep static percentage",
			path:        "/ofrep/v1/evaluate/flags/pct-flag",
			apiKey:      keyA,
			body:        ofrepCtx("u1"),
			wantStatus:  200,
			wantValue:   upperAName,
			wantVariant: "a",
			wantReason:  sTATIC,
		},
		{
			name:        "ofrep split user-1",
			path:        "/ofrep/v1/evaluate/flags/split-flag",
			apiKey:      keyA,
			body:        ofrepCtx(user1),
			wantStatus:  200,
			wantValue:   upperAName,
			wantVariant: "a",
			wantReason:  sPLIT,
		},
		{
			name:        "ofrep split user-0",
			path:        "/ofrep/v1/evaluate/flags/split-flag",
			apiKey:      keyA,
			body:        ofrepCtx(user0),
			wantStatus:  200,
			wantValue:   "A-B",
			wantVariant: "b",
			wantReason:  sPLIT,
		},
		{
			name:        "ofrep static boolean",
			path:        "/ofrep/v1/evaluate/flags/static-flag",
			apiKey:      keyB1,
			body:        ofrepCtx("u1"),
			wantStatus:  200,
			wantValue:   false,
			wantVariant: "f",
			wantReason:  sTATIC,
		},
		{
			name:        "ofrep flag of the other flag set is not found",
			path:        ofrepV1Evaluate3,
			apiKey:      keyA,
			body:        ofrepCtx("u1"),
			wantStatus:  404,
			wantErrCode: flagNotFoundCode,
		},
		{
			name:        "ofrep flag only in own flag set",
			path:        ofrepV1Evaluate3,
			apiKey:      keyB1,
			body:        ofrepCtx("u1"),
			wantStatus:  200,
			wantValue:   bOnly,
			wantVariant: "v",
			wantReason:  sTATIC,
		},
		{
			name:        "ofrep unknown flag",
			path:        ofrepV1Evaluate2,
			apiKey:      keyA,
			body:        ofrepCtx("u1"),
			wantStatus:  404,
			wantErrCode: flagNotFoundCode,
		},
		{
			name:        "ofrep missing context",
			path:        ofrepV1Evaluate4,
			apiKey:      keyA,
			body:        map[string]any{},
			wantStatus:  400,
			wantErrCode: "INVALID_CONTEXT",
		},
		{
			name:       "ofrep wrong api key",
			path:       ofrepV1Evaluate4,
			apiKey:     wrong,
			body:       ofrepCtx("u1"),
			wantStatus: 401,
		},
		{
			name:       "ofrep no api key",
			path:       ofrepV1Evaluate4,
			apiKey:     "",
			body:       ofrepCtx("u1"),
			wantStatus: 401,
		},
		// GOFF
		{
			name:        "goff targeting rule match",
			path:        v1FeatureRule,
			apiKey:      keyA,
			body:        goffCtx("u1", group, admin),
			wantStatus:  200,
			wantValue:   aOn,
			wantVariant: "on",
			wantReason:  tARGETINGMATCH,
		},
		{
			name:        "goff default rule",
			path:        v1FeatureRule,
			apiKey:      keyB1,
			body:        goffCtx("u1", group, user),
			wantStatus:  200,
			wantValue:   bOff,
			wantVariant: off,
			wantReason:  "DEFAULT",
		},
		{
			name:        "goff split user-0",
			path:        "/v1/feature/split-flag/eval",
			apiKey:      keyB2,
			body:        goffCtx(user0),
			wantStatus:  200,
			wantValue:   upperBName,
			wantVariant: "b",
			wantReason:  sPLIT,
		},
		{
			name:        "goff static percentage",
			path:        "/v1/feature/pct-flag/eval",
			apiKey:      keyA,
			body:        goffCtx("u1"),
			wantStatus:  200,
			wantValue:   upperAName,
			wantVariant: "a",
			wantReason:  sTATIC,
		},
		{
			name:        "goff unknown flag returns the default value",
			path:        "/v1/feature/nope/eval",
			apiKey:      keyA,
			body:        goffCtx("u1"),
			wantStatus:  200,
			wantValue:   "sdk-default",
			wantVariant: "SdkDefault",
			wantReason:  "ERROR",
			wantErrCode: flagNotFoundCode,
		},
		{
			name:       "goff wrong api key",
			path:       v1FeatureRule,
			apiKey:     wrong,
			body:       goffCtx("u1"),
			wantStatus: 401,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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

			assert.Equal(t, tt.wantValue, res.Body[value2], res.Raw)
			assert.Equal(t, tt.wantReason, res.Body["reason"], res.Raw)

			if isOFREP {
				assert.Equal(t, tt.wantVariant, res.Body["variant"], res.Raw)
			} else {
				assert.Equal(t, tt.wantVariant, res.Body["variationType"], res.Raw)
			}
		})
	}
}

func runS3FlagSetsBulkEval(t *testing.T, url string) {
	t.Helper()

	tests := []struct {
		body       map[string]any
		wantFlags  map[string]any // flag key -> value
		name       string
		path       string
		apiKey     string
		wantStatus int
	}{
		{
			name:       "ofrep bulk flag set a",
			path:       ofrepV1Evaluate,
			apiKey:     keyA,
			body:       ofrepCtx(user1, group, admin),
			wantStatus: 200,
			wantFlags: map[string]any{
				ruleFlag:    aOn,
				pctFlag:     upperAName,
				splitFlag:   upperAName,
				staticFlag:  false,
				"only-in-A": "A-only",
			},
		},
		{
			name:       "ofrep bulk flag set b",
			path:       ofrepV1Evaluate,
			apiKey:     keyB2,
			body:       ofrepCtx(user0, group, user),
			wantStatus: 200,
			wantFlags: map[string]any{
				ruleFlag:    bOff,
				pctFlag:     "B-A",
				splitFlag:   upperBName,
				staticFlag:  false,
				"only-in-B": bOnly,
			},
		},
		{
			name:       "ofrep bulk wrong api key",
			path:       ofrepV1Evaluate,
			apiKey:     wrong,
			body:       ofrepCtx("u"),
			wantStatus: 401,
		},
		{
			name:       "goff allflags flag set a",
			path:       v1Allflags,
			apiKey:     keyA,
			body:       goffCtx(user1, group, admin),
			wantStatus: 200,
			wantFlags: map[string]any{
				ruleFlag:    aOn,
				pctFlag:     upperAName,
				splitFlag:   upperAName,
				staticFlag:  false,
				"only-in-A": "A-only",
			},
		},
		{
			name:       "goff allflags flag set b",
			path:       v1Allflags,
			apiKey:     keyB1,
			body:       goffCtx(user0, group, user),
			wantStatus: 200,
			wantFlags: map[string]any{
				ruleFlag:    bOff,
				pctFlag:     "B-A",
				splitFlag:   upperBName,
				staticFlag:  false,
				"only-in-B": bOnly,
			},
		},
		{
			name:       "goff allflags wrong api key",
			path:       v1Allflags,
			apiKey:     wrong,
			body:       goffCtx("u"),
			wantStatus: 401,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := postJSON(t, url+tt.path, tt.apiKey, tt.body)
			require.Equal(t, tt.wantStatus, res.Status, res.Raw)

			if tt.wantStatus != 200 {
				return
			}

			got := map[string]any{}

			switch flags := res.Body["flags"].(type) {
			case []any: // OFREP: a list
				for _, f := range flags {
					item, isObj := f.(map[string]any)
					require.True(t, isObj)

					flagKey, isStr := item["key"].(string)
					require.True(t, isStr)

					got[flagKey] = item[value2]
				}
			case map[string]any: // GOFF: a map
				for k, f := range flags {
					item, isObj := f.(map[string]any)
					require.True(t, isObj)

					got[k] = item[value2]
				}
			}

			assert.Equal(t, tt.wantFlags, got, res.Raw)
		})
	}
}

func verifyS3FlagSetsReload(t *testing.T, srv *gopherstack.Server, bucket, url string) {
	t.Helper()

	srv.PutObject(t, bucket, flagsAYaml, e2eFlags("A", aOnV2))
	require.Eventually(t, func() bool {
		res := postJSON(
			t,
			url+ofrepV1Evaluate4,
			keyA,
			ofrepCtx("u1", group, admin),
		)

		return res.Status == 200 && res.Body[value2] == aOnV2
	}, 30*time.Second, 200*time.Millisecond, "new value of flag set a never served")
	// GOFF sees it too, and the other flag set is untouched.
	res := postJSON(t, url+v1FeatureRule, keyA, goffCtx("u1", group, admin))
	assert.Equal(t, aOnV2, res.Body[value2], res.Raw)
	res = postJSON(t, url+ofrepV1Evaluate4, keyB1, ofrepCtx("u1", group, admin))
	assert.Equal(t, bOn, res.Body[value2], res.Raw)

	// a flag removed from the file disappears, a flag added appears
	srv.PutObject(t, bucket, "flags/b.yaml", e2eFlags("B", bOn, `
brand-new:
  variations:
    v: new
  defaultRule:
    variation: v
`))
	require.Eventually(t, func() bool {
		brandNew := postJSON(t, url+"/ofrep/v1/evaluate/flags/brand-new", keyB1, ofrepCtx("u1"))

		return brandNew.Status == 200 && brandNew.Body[value2] == "new"
	}, 30*time.Second, 200*time.Millisecond, "new flag of flag set b never served")
}

//nolint:paralleltest // uses t.Setenv so the AWS SDK default config chain reads test endpoint/credentials
func TestE2E_RelayProxy_TopLevelS3Retriever(t *testing.T) {
	// no t.Parallel(): t.Setenv, see the comment at the top of the file.
	srv := gopherstack.Connect(t)
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
			wantValue: aOn,
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
			wantValue: aOn,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bucket := srv.CreateBucket(t, "top")

			for objKey, tag := range tt.objects {
				switch tag {
				case "A":
					srv.PutObject(t, bucket, objKey, e2eFlags("A", aOn))
				case "B":
					srv.PutObject(
						t,
						bucket,
						objKey,
						[]byte("only-in-B:\n  variations:\n    v: B-only\n  defaultRule:\n    variation: v\n"),
					)
				default:
					srv.PutObject(t, bucket, objKey, []byte(tag))
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

			res := postJSON(t, url+ofrepV1Evaluate4, topKey, ofrepCtx("u1", group, admin))
			require.Equal(t, 200, res.Status, res.Raw)
			assert.Equal(t, tt.wantValue, res.Body[value2])
			assert.Equal(t, tARGETINGMATCH, res.Body["reason"])

			if strings.Contains(tt.name, "folder") {
				res = postJSON(t, url+ofrepV1Evaluate3, topKey, ofrepCtx("u1"))
				require.Equal(t, 200, res.Status, res.Raw)
				assert.Equal(t, bOnly, res.Body[value2])
			}

			assert.Equal(t, 401, postJSON(t, url+ofrepV1Evaluate4, "nope", ofrepCtx("u1")).Status)

			// update
			first := ""

			for k, tag := range tt.objects {
				if tag == "A" {
					first = k
				}
			}

			srv.PutObject(t, bucket, first, e2eFlags("A", aOnV2))
			require.Eventually(t, func() bool {
				r := postJSON(t, url+v1FeatureRule, topKey, goffCtx("u1", group, admin))

				return r.Status == 200 && r.Body[value2] == aOnV2
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
	Value                   any
	Key, UserKey, Variation string
}

//nolint:paralleltest // uses t.Setenv so the AWS SDK default config chain reads test endpoint/credentials
func TestE2E_RelayProxy_ExportersToAWS(t *testing.T) {
	// no t.Parallel(): t.Setenv, see the comment at the top of the file.
	srv := gopherstack.Connect(t)
	srv.SetAwsEnv(t)

	bucket := srv.CreateBucket(t, "exp")
	srv.PutObject(t, bucket, flagsAYaml, e2eFlags("A", aOn))

	cfg := srv.MustAwsConfig(t)
	kinesisClient := kinesis.NewFromConfig(cfg)
	stream := gopherstack.UniqueName(t, "stream")
	_, err := kinesisClient.CreateStream(
		t.Context(),
		&kinesis.CreateStreamInput{StreamName: aws.String(stream), ShardCount: aws.Int32(1)},
	)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		out, describeStreamErr := kinesisClient.DescribeStream(
			t.Context(),
			&kinesis.DescribeStreamInput{StreamName: aws.String(stream)},
		)

		return describeStreamErr == nil && out.StreamDescription.StreamStatus == kinesistypes.StreamStatusActive
	}, 30*time.Second, 100*time.Millisecond)

	sqsClient := sqs.NewFromConfig(cfg)
	queue, err := sqsClient.CreateQueue(
		t.Context(),
		&sqs.CreateQueueInput{QueueName: aws.String(gopherstack.UniqueName(t, "queue"))},
	)
	require.NoError(t, err)

	url := startRelayProxyFromYAML(t, exportersYAML, map[string]any{
		"Bucket": bucket, "Stream": stream, "QueueURL": aws.ToString(queue.QueueUrl),
	})

	// evaluations from the three entry points, each with its own user
	wantUsers := map[string]string{} // user key -> expected value of rule-flag

	for i := range 4 {
		u := fmt.Sprintf("ofrep-%d", i)
		wantUsers[u] = aOn
		require.Equal(
			t,
			200,
			postJSON(t, url+ofrepV1Evaluate4, keyA, ofrepCtx(u, group, admin)).Status,
		)
	}

	for i := range 3 {
		u := fmt.Sprintf("goff-%d", i)
		wantUsers[u] = aOff
		require.Equal(
			t,
			200,
			postJSON(t, url+v1FeatureRule, keyA, goffCtx(u, group, user)).Status,
		)
	}

	sqsReceived := map[string]sinkEvent{} // only used by the sqs subtest

	sinks := []struct {
		read      func(t *testing.T) []sinkEvent
		wantUsers map[string]string
		name      string
	}{
		{
			name: "s3 exporter",
			read: func(t *testing.T) []sinkEvent {
				t.Helper()

				return readS3Events(t, srv, bucket)
			},
			wantUsers: wantUsers,
		},
		{
			name: "kinesis exporter",
			read: func(t *testing.T) []sinkEvent {
				t.Helper()

				return readKinesisEvents(t, kinesisClient, stream)
			},
			wantUsers: wantUsers,
		},
		{
			name: "sqs exporter",
			read: func(t *testing.T) []sinkEvent {
				t.Helper()

				return receiveSQS(t, sqsClient, aws.ToString(queue.QueueUrl), sqsReceived)
			},
			wantUsers: wantUsers,
		},
	}
	for _, s := range sinks {
		t.Run(s.name, func(t *testing.T) {
			assertSink(t, s.read, s.wantUsers)
		})
	}
}

// assertSink checks that the events read from a sink contain every evaluation made above.
func assertSink(t *testing.T, read func(t *testing.T) []sinkEvent, wantUsers map[string]string) {
	t.Helper()

	var got []sinkEvent
	// polled on the test goroutine: read uses require, which must not run off it.
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	deadline := time.After(30 * time.Second)

	for {
		got = read(t)
		users := map[string]bool{}

		for _, e := range got {
			if e.Key == ruleFlag {
				users[e.UserKey] = true
			}
		}

		if len(users) >= len(wantUsers) {
			break
		}

		select {
		case <-deadline:
			require.Fail(t, "not every evaluation reached the sink", "events received: %d", len(got))
		case <-ticker.C:
		}
	}

	seen := map[string]sinkEvent{}

	for _, e := range got {
		if e.Key == ruleFlag {
			seen[e.UserKey] = e
		}
	}

	for u, v := range wantUsers {
		e, ok := seen[u]
		if assert.True(t, ok, "no event for user %s", u) {
			assert.Equal(t, v, e.Value, "user %s", u)
			assert.Equal(t, map[string]string{aOn: "on", aOff: off}[v], e.Variation, "user %s", u)
		}
	}
}

// readKinesisEvents reads every event of the stream, from the oldest record of each shard.
func readKinesisEvents(t *testing.T, kinesisClient *kinesis.Client, stream string) []sinkEvent {
	t.Helper()

	desc, describeStreamErr := kinesisClient.DescribeStream(
		t.Context(),
		&kinesis.DescribeStreamInput{StreamName: aws.String(stream)},
	)
	require.NoError(t, describeStreamErr)

	var res []sinkEvent

	for _, shard := range desc.StreamDescription.Shards {
		iter, getShardIteratorErr := kinesisClient.GetShardIterator(
			t.Context(),
			&kinesis.GetShardIteratorInput{
				StreamName: aws.String(
					stream,
				),
				ShardId:           shard.ShardId,
				ShardIteratorType: kinesistypes.ShardIteratorTypeTrimHorizon,
			},
		)
		require.NoError(t, getShardIteratorErr)
		out, getShardIteratorErr := kinesisClient.GetRecords(
			t.Context(),
			&kinesis.GetRecordsInput{ShardIterator: iter.ShardIterator, Limit: aws.Int32(1000)},
		)
		require.NoError(t, getShardIteratorErr)

		for _, r := range out.Records {
			res = append(res, decodeFeatureEvent(t, r.Data))
		}
	}

	return res
}

// readS3Events reads every event that the S3 exporter wrote.
func readS3Events(t *testing.T, srv *gopherstack.Server, bucket string) []sinkEvent {
	t.Helper()

	var res []sinkEvent

	for _, k := range srv.ListKeys(t, bucket, "exports/a/") {
		require.True(t, strings.HasSuffix(k, ".json"), k)

		for line := range strings.SplitSeq(strings.TrimSpace(string(srv.GetObject(t, bucket, k))), "\n") {
			res = append(res, decodeFeatureEvent(t, []byte(line)))
		}
	}

	return res
}

// receiveSQS receives and deletes the messages available, and adds them to acc (message id -> event),
// so the successive polls of an Eventually accumulate everything received.
func receiveSQS(t *testing.T, sqsClient *sqs.Client, queueURL string, acc map[string]sinkEvent) []sinkEvent {
	t.Helper()
	out, err := sqsClient.ReceiveMessage(t.Context(), &sqs.ReceiveMessageInput{
		QueueUrl: aws.String(queueURL), MaxNumberOfMessages: 10, WaitTimeSeconds: 1,
	})
	require.NoError(t, err)

	for _, m := range out.Messages {
		e := decodeFeatureEvent(t, []byte(aws.ToString(m.Body)))
		acc[aws.ToString(m.MessageId)] = e
		_, deleteMessageErr := sqsClient.DeleteMessage(
			t.Context(),
			&sqs.DeleteMessageInput{QueueUrl: aws.String(queueURL), ReceiptHandle: m.ReceiptHandle},
		)
		require.NoError(t, deleteMessageErr)
	}

	res := make([]sinkEvent, 0, len(acc))
	for _, e := range acc {
		res = append(res, e)
	}

	slices.SortFunc(res, func(a, b sinkEvent) int { return cmp.Compare(a.UserKey, b.UserKey) })

	return res
}

func decodeFeatureEvent(t *testing.T, b []byte) sinkEvent {
	t.Helper()

	var e exporter.FeatureEvent
	require.NoError(t, json.Unmarshal(b, &e), string(b))

	return sinkEvent{Key: e.Key, UserKey: e.UserKey, Variation: e.Variation, Value: e.Value}
}
