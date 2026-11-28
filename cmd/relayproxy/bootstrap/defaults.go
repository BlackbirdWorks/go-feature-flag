package bootstrap

import "time"

// The defaults table. docs/relay-proxy/bootstrap-config.md documents the same values.
const (
	DefaultAPIAddress     = ":1031"
	DefaultLogLevel       = "info"
	DefaultLogFormat      = "json"
	DefaultServiceName    = "semaphore"
	DefaultSampler        = "parentbased_always_off"
	DefaultPollInterval   = 60 * time.Second
	DefaultJitter         = 0.1
	DefaultConnectTimeout = 10 * time.Second
	DefaultRequestTimeout = 30 * time.Second
	DefaultMaxAttempts    = 3
	DefaultMaxBackoff     = 20 * time.Second
	DefaultChannel        = "stable"
	DefaultMaxObjects     = 20000
	DefaultFlushInterval  = 60 * time.Second
	DefaultMaxEvents      = 100000
	DefaultExportFormat   = "json"
	// DefaultShutdownTimeout bounds the stop that follows the drain delay.
	DefaultShutdownTimeout = 15 * time.Second
	// DefaultBlockTimeout is the wait of the block overflow policy of an exporter for room in its queue.
	DefaultBlockTimeout = 50 * time.Millisecond
	// MaxBlockTimeout is the upper bound of the wait of the block overflow policy.
	MaxBlockTimeout = 10 * time.Second
	// DefaultExporterDrainTimeout is the deadline of the final flush of an exporter that leaves.
	DefaultExporterDrainTimeout = 10 * time.Second
	// DefaultRetryAttempts is the attempts of a batch of an exporter, the first included.
	DefaultRetryAttempts = 5
	// DefaultRetryInitialBackoff is the wait before the first retry of an export.
	DefaultRetryInitialBackoff = 200 * time.Millisecond
	// DefaultRetryMaxBackoff caps one wait of the retry of an export.
	DefaultRetryMaxBackoff = 5 * time.Second
	// DefaultRetryMaxElapsed caps the waits of the retry of one batch.
	DefaultRetryMaxElapsed = 30 * time.Second
	// MaxShutdownDuration is the upper bound of the drain delay and of the shutdown timeout.
	MaxShutdownDuration = 10 * time.Minute
)

func orDefault[T comparable](v *T, def T) *T {
	if v == nil {
		return &def
	}

	return v
}

func (c *Config) applyDefaults() {
	c.Server.applyDefaults()
	c.Observability.applyDefaults()

	for name, conn := range c.Connections {
		conn.applyDefaults()
		c.Connections[name] = conn
	}

	for name := range c.Exporters {
		exporter := c.Exporters[name]
		exporter.applyDefaults()
		c.Exporters[name] = exporter
	}

	if c.Segments != nil {
		c.Segments.applyDefaults()
	}

	if c.ControlPlane != nil {
		manifest := &c.ControlPlane.Manifest
		if manifest.Channel == "" {
			manifest.Channel = DefaultChannel
		}

		manifest.PollInterval = orDefault(manifest.PollInterval, Duration(DefaultPollInterval))
		manifest.Jitter = orDefault(manifest.Jitter, DefaultJitter)
		manifest.MaxObjects = orDefault(manifest.MaxObjects, DefaultMaxObjects)
	}
}

func (s *Server) applyDefaults() {
	s.Shutdown.DrainDelay = orDefault(s.Shutdown.DrainDelay, Delay(0))
	s.Shutdown.Timeout = orDefault(s.Shutdown.Timeout, Duration(DefaultShutdownTimeout))

	if len(s.Listeners) == 0 {
		s.Listeners = []Listener{{Name: RoleAPI, Address: DefaultAPIAddress}}
	}

	for i := range s.Listeners {
		listener := &s.Listeners[i]
		if listener.Role == "" {
			listener.Role = RoleAPI
		}

		if len(listener.Protocols) == 0 {
			listener.Protocols = []string{ProtocolHTTP1}
		}
	}
}

func (o *Observability) applyDefaults() {
	if o.Logging.Level == "" {
		o.Logging.Level = DefaultLogLevel
	}

	if o.Logging.Format == "" {
		o.Logging.Format = DefaultLogFormat
	}

	o.Metrics.Enabled = orDefault(o.Metrics.Enabled, true)

	if o.Tracing.ServiceName == "" {
		o.Tracing.ServiceName = DefaultServiceName
	}

	if o.Tracing.Sampler == "" {
		o.Tracing.Sampler = DefaultSampler
	}
}

func (a *AWS) applyDefaults() {
	if a.Credentials == nil {
		a.Credentials = &Credentials{}
	}

	if a.Credentials.Type == "" {
		a.Credentials.Type = CredentialsChain
	}

	if a.Timeouts == nil {
		a.Timeouts = &Timeouts{}
	}

	a.Timeouts.Connect = orDefault(a.Timeouts.Connect, Duration(DefaultConnectTimeout))
	a.Timeouts.Request = orDefault(a.Timeouts.Request, Duration(DefaultRequestTimeout))

	if a.Retry == nil {
		a.Retry = &Retry{}
	}

	a.Retry.MaxAttempts = orDefault(a.Retry.MaxAttempts, DefaultMaxAttempts)
	a.Retry.MaxBackoff = orDefault(a.Retry.MaxBackoff, Duration(DefaultMaxBackoff))
}

func (c Connection) applyDefaults() {
	if k := c.Valkey; k != nil && k.Pipelining == "" {
		k.Pipelining = PipeliningAlways
	}

	if a := c.aws(); a != nil {
		a.applyDefaults()
	}

	if s3Conn := c.S3; s3Conn != nil {
		if s3Conn.Poll == nil {
			s3Conn.Poll = &Poll{}
		}

		s3Conn.Poll.Interval = orDefault(s3Conn.Poll.Interval, Duration(DefaultPollInterval))
		s3Conn.Poll.Jitter = orDefault(s3Conn.Poll.Jitter, DefaultJitter)

		if s3Conn.RequestChecksumCalculation == "" {
			s3Conn.RequestChecksumCalculation = ChecksumWhenSupported
		}

		if s3Conn.ResponseChecksumValidation == "" {
			s3Conn.ResponseChecksumValidation = ChecksumWhenSupported
		}
	}
}

func (e *Exporter) applyDefaults() {
	e.FlushInterval = orDefault(e.FlushInterval, Duration(DefaultFlushInterval))
	e.MaxEventInMemory = orDefault(e.MaxEventInMemory, DefaultMaxEvents)
	e.BlockTimeout = orDefault(e.BlockTimeout, Duration(DefaultBlockTimeout))
	e.DrainTimeout = orDefault(e.DrainTimeout, Duration(DefaultExporterDrainTimeout))

	if e.OverflowPolicy == "" {
		e.OverflowPolicy = OverflowDropNewest
	}

	if e.Retry == nil {
		e.Retry = &ExporterRetry{}
	}

	e.Retry.MaxAttempts = orDefault(e.Retry.MaxAttempts, DefaultRetryAttempts)
	e.Retry.InitialBackoff = orDefault(e.Retry.InitialBackoff, Duration(DefaultRetryInitialBackoff))
	e.Retry.MaxBackoff = orDefault(e.Retry.MaxBackoff, Duration(DefaultRetryMaxBackoff))
	e.Retry.MaxElapsed = orDefault(e.Retry.MaxElapsed, Duration(DefaultRetryMaxElapsed))

	switch e.Kind {
	case ExporterFile, ExporterS3, ExporterGoogleStorage, ExporterAzure:
		if e.Format == "" {
			e.Format = DefaultExportFormat
		}
	}
}
