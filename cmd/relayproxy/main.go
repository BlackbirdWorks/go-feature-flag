package main

import (
	"context"
	"crypto/fips140"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/pflag"
	"go.uber.org/zap"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api/opentelemetry"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/docs"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	stream "github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
	"github.com/blackbirdworks/semaphore/cmdhelpers/buildinfo"
	"github.com/blackbirdworks/semaphore/cmdhelpers/log"
	"github.com/blackbirdworks/semaphore/notifier"
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

// versionRequested reports whether args ask for --version / -v. It uses a flag set of its
// own so that the flag never reaches the configuration loader.
func versionRequested(args []string) bool {
	f := pflag.NewFlagSet("version", pflag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.ParseErrorsAllowlist.UnknownFlags = true
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

// @title Semaphore relay proxy endpoints
// @description.markdown
// @contact.name Semaphore relay proxy
// @license.name MIT
// @license.url https://github.com/BlackbirdWorks/semaphore/blob/main/LICENSE
// @BasePath /
// @securitydefinitions.apikey ApiKeyAuth
// @in header
// @name Authorization
// @description Use configured APIKeys in yaml config as authorization keys, disabled when this yaml config is not set.
// @securitydefinitions.apikey XApiKeyAuth
// @in header
// @name X-API-Key
// @description Use configured APIKeys in yaml config as authorization keys via X-API-Key header,
// @description disabled when this yaml config is not set.
func main() {
	if versionRequested(os.Args[1:]) {
		fmt.Print(versionText()) //nolint:forbidigo // --version output on stdout

		return
	}

	// Init pFlag for config file
	f := pflag.NewFlagSet("config", pflag.ContinueOnError)
	f.String("config", "", "Location of your config file")
	_ = f.Parse(os.Args[1:])

	// Init logger
	logger := log.InitLogger()
	defer func() { _ = logger.ZapLogger.Sync() }()

	// Loading the configuration in viper
	proxyConf, err := config.New(f, logger.ZapLogger, version)
	if err != nil {
		logger.ZapLogger.Fatal("error while reading configuration", zap.Error(err))
	}
	defer func() {
		if err := proxyConf.StopConfigChangeWatcher(); err != nil {
			logger.ZapLogger.Error("error while stopping the configuration watcher", zap.Error(err))
		}
	}()

	if err := proxyConf.IsValid(); err != nil {
		logger.ZapLogger.Fatal("configuration error", zap.Error(err))
	}

	if !proxyConf.HideBanner {
		fmt.Printf(banner+"\n", version) //nolint:forbidigo // startup banner on stdout
	}

	// Update the logger's format and level from the config
	logger.Update(proxyConf.LogFormat, proxyConf.ZapLogLevel())

	// Surface whether the binary is running with FIPS 140-3 validated crypto.
	// True only for builds produced with GOFIPS140 (see `make build-relayproxy-fips`).
	logger.ZapLogger.Info("crypto mode", zap.Bool("fips140", fips140.Enabled()))

	// Init swagger
	docs.SwaggerInfo.Version = proxyConf.Version
	docs.SwaggerInfo.Host = fmt.Sprintf("%s:%d", proxyConf.SwaggerHost(), proxyConf.ServerPort(logger.ZapLogger))

	// Initialize metrics
	metricsV2, err := metric.NewMetrics(metric.MetricsOpts{
		Version:                   version,
		EnableBulkMetricFlagNames: proxyConf.EnableBulkMetricFlagNames,
	})
	if err != nil {
		logger.ZapLogger.Error("impossible to initialize prometheus metrics", zap.Error(err))
	}

	// Init services
	wsService := stream.NewWebsocketService(stream.WithFlagDetails(!proxyConf.DisableFlagDetailsInStream))
	sseService := stream.NewSSEService()
	prometheusNotifier := metric.NewPrometheusNotifier(metricsV2)

	flagsetManager, err := service.NewFlagsetManager(proxyConf, logger.ZapLogger, []notifier.Notifier{
		prometheusNotifier,
	}, sseService, wsService)
	if err != nil {
		logger.ZapLogger.Error(
			"impossible to start Semaphore, we are not able to initialize the retrieval of flags",
			zap.Error(err),
		)

		return
	}
	if err := metricsV2.RegisterFlagLoaded(flagsetManager); err != nil {
		logger.ZapLogger.Error("impossible to register the flag_loaded metric", zap.Error(err))
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
	apiServer := api.New(proxyConf, services, logger.ZapLogger, api.WithGlobalTelemetry())

	defer shutdown(logger.ZapLogger, &apiServer, wsService, sseService, flagsetManager)
	apiServer.StartWithContext(context.Background())
}

// shutdown stops the relay proxy: streaming clients disconnect first, then the HTTP server stops,
// and finally the flagsets are closed.
func shutdown(
	logger *zap.Logger,
	apiServer interface{ Stop(ctx context.Context) },
	wsService stream.WebsocketService,
	sseService stream.SSEService,
	flagsetManager service.FlagsetManager,
) {
	logger.Info("Stopping API server")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wsService.Close()
	sseService.Close()
	apiServer.Stop(ctx)
	// Closing the flagsets flushes the exporters, once the server no longer accepts requests.
	flagsetManager.Close()
}
