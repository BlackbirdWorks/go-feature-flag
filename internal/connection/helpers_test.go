package connection_test

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blackbirdworks/semaphore/internal/connection"
)

// errScriptExhausted is returned by a scripted transport that is asked for more requests than it has steps.
var errScriptExhausted = errors.New("scripted transport: no step left")

// step is one scripted HTTP answer; err makes the transport fail instead.
type step struct {
	err    error
	header http.Header
	body   string
	status int
}

// scriptedTransport answers the requests in order and records them.
type scriptedTransport struct {
	steps    []step
	requests []*http.Request
	mu       sync.Mutex
}

func newScripted(steps ...step) *scriptedTransport { return &scriptedTransport{steps: steps} }

func (s *scriptedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.requests = append(s.requests, req)

	if len(s.requests) > len(s.steps) {
		return nil, errScriptExhausted
	}

	cur := s.steps[len(s.requests)-1]
	if cur.err != nil {
		return nil, cur.err
	}

	header := cur.header.Clone()
	if header == nil {
		header = http.Header{}
	}

	header.Set("Content-Length", strconv.Itoa(len(cur.body)))

	return &http.Response{
		StatusCode: cur.status,
		Status:     strconv.Itoa(cur.status) + " " + http.StatusText(cur.status),
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(cur.body)),
		Request:    req,
	}, nil
}

func (s *scriptedTransport) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.requests)
}

// roundTripFunc adapts a function to an [http.RoundTripper].
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// observation is one call of the observer.
type observation struct {
	Connection string
	Operation  string
	Result     string
}

// recorder is a RequestObserver that keeps the calls in order.
type recorder struct {
	obs []observation
	mu  sync.Mutex
}

func (r *recorder) Observe(conn, operation, result string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.obs = append(r.obs, observation{Connection: conn, Operation: operation, Result: result})
}

func (r *recorder) all() []observation {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]observation(nil), r.obs...)
}

const (
	// valueHello is the body of the objects the scripted transport serves.
	valueHello = "hello"
	// testSTSEndpoint is the STS endpoint of the credential tests.
	testSTSEndpoint = "http://sts.test"
	testRoleARN     = "arn:r"
	fieldRegion     = "region"
	testEndpoint    = "http://s3.test"
	envDefaultsMode = "AWS_DEFAULTS_MODE"
	envProfile      = "AWS_PROFILE"
	keyChain        = "AKIDCHAIN"
	missingProfile  = "profile-that-does-not-exist"
	nameStatic      = "static"
	nameWebIdentity = "webIdentity"
	valBogus        = "bogus"
	verbPlusV       = "%+v"
	verbGoV         = "%#v"
	pemCertificate  = "CERTIFICATE"
	roleKeyID       = "ASIAROLE"
	hostSTS         = "sts.test"
	headerType      = "Content-Type"
	testCAPath      = "/ca.pem"
	testRoleFull    = "arn:aws:iam::1:role/r"
	testSession     = "sess"
	formAction      = "Action"
	formRoleArn     = "RoleArn"
	testRegionEU    = "eu-west-1"
	connC           = "c"
	connOne         = "one"
	connTwo         = "two"
	fieldCreds      = "credentials"
	bucketOther     = "other"
)

// The operation and result labels, spelled out so that renaming a value of the package fails the tests.
const (
	opGet          = "get"
	opPut          = "put"
	opHead         = "head"
	opList         = "list"
	opDelete       = "delete"
	opOther        = "other"
	resOK          = "ok"
	resNotModified = "not_modified"
	resNotFound    = "not_found"
	resThrottled   = "throttled"
	resClientError = "client_error"
	resServerError = "server_error"
	resError       = "error"
	resRedirect    = "redirect"
)

const (
	noSuchKeyXML = `<?xml version="1.0" encoding="UTF-8"?><Error><Code>NoSuchKey</Code>` +
		`<Message>The specified key does not exist.</Message></Error>`
	slowDownXML = `<?xml version="1.0" encoding="UTF-8"?><Error><Code>SlowDown</Code>` +
		`<Message>Please reduce your request rate.</Message></Error>`
	accessDeniedXML = `<?xml version="1.0" encoding="UTF-8"?><Error><Code>AccessDenied</Code>` +
		`<Message>Access Denied</Message></Error>`
	internalErrorXML = `<?xml version="1.0" encoding="UTF-8"?><Error><Code>InternalError</Code>` +
		`<Message>We encountered an internal error.</Message></Error>`
	tooManyXML = `<?xml version="1.0" encoding="UTF-8"?><Error><Code>Throttling</Code>` +
		`<Message>Rate exceeded</Message></Error>`
)

// testS3 is a valid S3 spec whose retries do not wait.
func testS3() *connection.S3Spec {
	return &connection.S3Spec{
		Endpoint: testEndpoint,
		Region:   "us-east-1",
		Credentials: connection.Credentials{
			Type:            connection.CredentialsStatic,
			AccessKeyID:     connection.NewSecret("AKIDTEST"),
			SecretAccessKey: connection.NewSecret("secret-test"),
		},
		Retry:     connection.Retry{MaxAttempts: 3, MaxBackoff: time.Millisecond},
		Bucket:    "bucket",
		PathStyle: true,
	}
}

func stringsReader(s string) io.Reader { return strings.NewReader(s) }
