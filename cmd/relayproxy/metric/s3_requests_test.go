package metric_test

import (
	"strings"
	"testing"

	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/internal/connection"
)

func TestS3Requests_Observe(t *testing.T) {
	t.Parallel()

	type obs struct{ conn, op, result string }

	tests := []struct {
		name string
		want string
		obs  []obs
	}{
		{
			name: "attempts are counted one by one, a 304 is not_modified",
			obs: []obs{
				{connControl, connection.OperationGet, connection.ResultServerError},
				{connControl, connection.OperationGet, connection.ResultOK},
				{connControl, connection.OperationGet, connection.ResultNotModified},
				{connControl, connection.OperationGet, connection.ResultNotModified},
			},
			want: `
# HELP semaphore_s3_requests_total ` + help + `
# TYPE semaphore_s3_requests_total counter
semaphore_s3_requests_total{connection="control",operation="get",result="not_modified"} 2
semaphore_s3_requests_total{connection="control",operation="get",result="ok"} 1
semaphore_s3_requests_total{connection="control",operation="get",result="server_error"} 1
`,
		},
		{
			name: "connections are separate series",
			obs: []obs{
				{"a", connection.OperationGet, connection.ResultOK},
				{"b", connection.OperationList, connection.ResultNotFound},
			},
			want: `
# HELP semaphore_s3_requests_total ` + help + `
# TYPE semaphore_s3_requests_total counter
semaphore_s3_requests_total{connection="a",operation="get",result="ok"} 1
semaphore_s3_requests_total{connection="b",operation="list",result="not_found"} 1
`,
		},
		{
			name: "values outside the closed sets fall into other",
			obs: []obs{
				{"a", "GetObject/some-key", "418"},
				{"a", "delete-everything", connection.ResultOK},
			},
			want: `
# HELP semaphore_s3_requests_total ` + help + `
# TYPE semaphore_s3_requests_total counter
semaphore_s3_requests_total{connection="a",operation="other",result="ok"} 1
semaphore_s3_requests_total{connection="a",operation="other",result="other"} 1
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reg := prom.NewRegistry()
			requests, err := metric.NewS3Requests(reg)
			require.NoError(t, err)

			for _, o := range tt.obs {
				requests.Observe(o.conn, o.op, o.result)
			}

			assert.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(tt.want), "semaphore_s3_requests_total"))
		})
	}
}

func TestNewS3Requests_RegisteredTwiceFails(t *testing.T) {
	t.Parallel()

	reg := prom.NewRegistry()
	_, err := metric.NewS3Requests(reg)
	require.NoError(t, err)

	_, err = metric.NewS3Requests(reg)
	assert.Error(t, err)
}

func TestNewMetrics_DoesNotExposeS3Requests(t *testing.T) {
	t.Parallel()

	metrics, err := metric.NewMetrics()
	require.NoError(t, err)

	families, err := metrics.Registry.Gather()
	require.NoError(t, err)

	for _, family := range families {
		assert.NotEqual(t, "semaphore_s3_requests_total", family.GetName())
	}
}

const (
	help = "S3 requests by connection, operation (get, list, head, put, delete, other) and result (ok, " +
		"not_modified, not_found, throttled, client_error, redirect, server_error, error); " +
		"one count per attempt of the SDK."
	connControl = "control"
)
