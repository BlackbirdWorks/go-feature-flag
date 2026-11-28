// Package segments holds the load and chaos tests of the dynamic segments against a real Valkey and a real relay
// proxy, started from the harness in internal/loadtest/relay. It has no code: the tests are in its test files, behind
// the docker tag (the chaos tests) and the bench and docker tags (the load test, `task test-segment-load`).
package segments
