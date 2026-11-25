package main

import (
	"context"
	"crypto/fips140"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/pflag"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api/opentelemetry"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
	"github.com/blackbirdworks/semaphore/cmdhelpers/buildinfo"
	"github.com/blackbirdworks/semaphore/cmdhelpers/cmdlog"
	"github.com/blackbirdworks/semaphore/notifier"
)

const (
	// shutdownTimeout is how long the shutdown of the servers can last.
	shutdownTimeout = 5 * time.Second
)

// version, commit and date are overridden by GoReleaser during the build (-X main.version=...).
var (
	version = buildinfo.DevVersion
	commit  string //nolint:gochecknoglobals // set with -ldflags -X main.commit
	date    string //nolint:gochecknoglobals // set with -ldflags -X main.date
)

// versionText is what --version prints.
func versionText() string {
	return buildinfo.Text("semaphore", buildinfo.Linked{Version: version, Commit: commit, Date: date},
		buildinfo.Read(), fips140.Enabled())
}

// newConfigFlagSet declares the command line flags. config is declared so that its value is
// consumed as a value even when it starts with a dash; unknown flags (--version, -v) are skipped
// instead of stopping the parse and losing --config.
func newConfigFlagSet() *pflag.FlagSet {
	f := pflag.NewFlagSet("config", pflag.ContinueOnError)
	f.String("config", "", "Location of your config file")
	f.ParseErrorsAllowlist.UnknownFlags = true

	return f
}

// versionRequested reports whether args ask for --version / -v. It uses a flag set of its
// own so that the flag never reaches the configuration loader.
func versionRequested(args []string) bool {
	f := newConfigFlagSet()
	f.SetOutput(io.Discard)
	show := f.BoolP("version", "v", false, "Print the version and exit")
	_ = f.Parse(args)

	return *show
}

const banner = `█▀▀ █▀█   █▀▀ █▀▀ ▄▀█ ▀█▀ █ █ █▀█ █▀▀   █▀▀ █   ▄▀█ █▀▀
█▄█ █▄█   █▀  ██▄ █▀█  █  █▄█ █▀▄ ██▄   █▀  █▄▄ █▀█ █▄█

     █▀█ █▀▀ █   ▄▀█ █▄█   █▀█ █▀█ █▀█ ▀▄▀ █▄█
     █▀▄ ██▄ █▄▄ █▀█  █    █▀▀ █▀▄ █▄█ █ █  █ 

Semaphore Relay Proxy - Version %s
_____________________________________________`

func main() {
	os.Exit(run(func() { os.Exit(1) }))
}

// run starts the relay proxy and returns the exit code of the process. exit ends the process, the server calls it
// after a fatal error.
func run(exit func()) int {
	if versionRequested(os.Args[1:]) {
		fmt.Print(versionText()) //nolint:forbidigo // --version output on stdout

		return 0
	}

	// Init pFlag for config file
	flagSet := newConfigFlagSet()
	_ = flagSet.Parse(os.Args[1:])

	// Init logger
	logger := cmdlog.NewDefault()

	// Loading the configuration in viper
	proxyConf, err := config.New(flagSet, logger.Slog, version)
	if err != nil {
		logFatal(logger.Slog, "error while reading configuration", cmdlog.Err(err))

		return 1
	}
	defer func() {
		stopConfigChangeWatcherErr := proxyConf.StopConfigChangeWatcher()
		if stopConfigChangeWatcherErr != nil {
			logger.Slog.LogAttrs(context.Background(), slog.LevelError,
				"error while stopping the configuration watcher",
				cmdlog.Err(stopConfigChangeWatcherErr),
			)
		}
	}()

	isValidErr := proxyConf.IsValid()
	if isValidErr != nil {
		logFatal(logger.Slog, "configuration error", cmdlog.Err(isValidErr))

		return 1
	}

	printBanner(proxyConf)

	// Update the logger's format and level from the config
	updateErr := logger.Update(proxyConf.LogFormat, proxyConf.LogLevel)
	if updateErr != nil {
		logFatal(logger.Slog, "configuration error", cmdlog.Err(updateErr))

		return 1
	}

	logCryptoMode(logger.Slog)

	// Initialize metrics
	metricsV2 := newMetrics(logger.Slog, proxyConf)

	// Init services
	wsService := stream.NewWebsocketService(stream.WithFlagDetails(!proxyConf.DisableFlagDetailsInStream))
	sseService := stream.NewSSEService()
	prometheusNotifier := metric.NewPrometheusNotifier(metricsV2)

	flagsetManager, err := service.NewFlagsetManager(proxyConf, logger.Slog, []notifier.Notifier{
		prometheusNotifier,
	}, sseService, wsService)
	if err != nil {
		logger.Slog.LogAttrs(context.Background(), slog.LevelError,
			"impossible to start Semaphore, we are not able to initialize the retrieval of flags",
			cmdlog.Err(err),
		)

		return 0
	}

	registerFlagLoadedErr := metricsV2.RegisterFlagLoaded(flagsetManager)
	if registerFlagLoadedErr != nil {
		logger.Slog.LogAttrs(
			context.Background(),
			slog.LevelError,
			"impossible to register the flag_loaded metric",
			cmdlog.Err(registerFlagLoadedErr),
		)
	}

	services := service.Services{
		MonitoringService: service.NewMonitoring(flagsetManager),
		WebsocketService:  wsService,
		SSEService:        sseService,
		FlagsetManager:    flagsetManager,
		Metrics:           metricsV2,
	}
	// Init API server
	// The OpenTelemetry endpoint and the global tracer provider are process-wide: they are set here only.
	opentelemetry.ExportEnvironment(proxyConf)
	apiServer := api.New(proxyConf, &services, logger.Slog, api.WithGlobalTelemetry(), api.WithExit(exit))

	defer shutdown(logger.Slog, &apiServer, wsService, sseService, flagsetManager)

	apiServer.StartWithContext(context.Background())

	return 0
}

// logCryptoMode surfaces whether the binary is running with FIPS 140-3 validated crypto.
// True only for builds produced with GOFIPS140 (see `make build-relayproxy-fips`).
func logCryptoMode(logger *slog.Logger) {
	logger.LogAttrs(context.Background(), slog.LevelInfo, "crypto mode", slog.Bool("fips140", fips140.Enabled()))
}

// printBanner prints the startup banner on stdout, unless the configuration hides it.
func printBanner(proxyConf *config.Config) {
	if !proxyConf.HideBanner {
		fmt.Printf(banner+"\n", version) //nolint:forbidigo // startup banner on stdout
	}
}

// newMetrics initializes the metrics. When it fails, the error is logged and the metrics stay unset.
func newMetrics(logger *slog.Logger, proxyConf *config.Config) *metric.Metrics {
	metrics, err := metric.NewMetrics(metric.MetricsOpts{
		Version:                   version,
		EnableBulkMetricFlagNames: proxyConf.EnableBulkMetricFlagNames,
	})
	if err != nil {
		logger.LogAttrs(
			context.Background(),
			slog.LevelError,
			"impossible to initialize prometheus metrics",
			cmdlog.Err(err),
		)
	}

	return metrics
}

// logFatal logs the message at the fatal level.
func logFatal(logger *slog.Logger, msg string, attrs ...slog.Attr) {
	logger.LogAttrs(
		context.Background(),
		cmdlog.LevelFatal,
		"fatal error in relay proxy",
		append([]slog.Attr{slog.String("reason", msg)}, attrs...)...)
}

// shutdown stops the relay proxy: streaming clients disconnect first, then the HTTP server stops,
// and finally the flagsets are closed.
func shutdown(
	logger *slog.Logger,
	apiServer interface{ Stop(ctx context.Context) },
	wsService stream.WebsocketService,
	sseService stream.SSEService,
	flagsetManager service.FlagsetManager,
) {
	logger.Info("Stopping API server")

	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	wsService.Close()
	sseService.Close()
	apiServer.Stop(ctx)
	// Closing the flagsets flushes the exporters, once the server no longer accepts requests.
	flagsetManager.Close()
}
