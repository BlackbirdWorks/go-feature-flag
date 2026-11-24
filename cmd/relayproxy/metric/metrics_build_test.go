//go:build docker

package metric_test

import (
	"bytes"
	"fmt"
	"runtime"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
)

func TestMetrics_BuildInfo(t *testing.T) {
	t.Parallel()

	version := "9.9.9"

	metricSrv, err := metric.NewMetrics(metric.MetricsOpts{Version: version})
	require.NoError(t, err)

	helpText := "A metric with a constant '1' value labeled by version, revision, branch, goversion " +
		"from which semaphore was built, and the goos and goarch for the build."
	expected := fmt.Sprintf(`
		# HELP semaphore_build_info %s
		# TYPE semaphore_build_info gauge
		semaphore_build_info{branch="",goarch="%s",goos="%s",goversion="%s",revision="unknown",tags="docker",version="%s"} 1
	`, helpText, runtime.GOARCH, runtime.GOOS, runtime.Version(), version)

	require.NoError(
		t,
		testutil.GatherAndCompare(metricSrv.Registry, bytes.NewReader([]byte(expected)), "semaphore_build_info"),
	)
}
