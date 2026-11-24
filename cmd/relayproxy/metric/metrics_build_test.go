//go:build docker

package metric_test

import (
	"bytes"
	"fmt"
	"runtime"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	promversion "github.com/prometheus/common/version"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
)

//nolint:paralleltest,tparallel // assigns to the package-level variable promversion.Version
func TestMetrics_BuildInfo(t *testing.T) {
	version := "9.9.9"
	promversion.Version = version

	metricSrv, err := metric.NewMetrics()
	require.NoError(t, err)

	expected := fmt.Sprintf(`
		# HELP semaphore_build_info A metric with a constant '1' value labeled by version, revision, branch, goversion from which semaphore was built, and the goos and goarch for the build.
		# TYPE semaphore_build_info gauge
		semaphore_build_info{branch="",goarch="%s",goos="%s",goversion="%s",revision="unknown",tags="docker",version="%s"} 1
	`, runtime.GOARCH, runtime.GOOS, runtime.Version(), version)

	require.NoError(
		t,
		testutil.GatherAndCompare(metricSrv.Registry, bytes.NewReader([]byte(expected)), "semaphore_build_info"),
	)
}
