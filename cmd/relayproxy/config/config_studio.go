package config

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/v2"

	cmdlog "github.com/blackbirdworks/semaphore/cmdhelpers/log"
	httpprovider "github.com/blackbirdworks/semaphore/internal/koanfhttpprovider"
)

// Studio holds the configuration to load the flagSets from Semaphore Studio.
// When URL is set, the flagSets only come from Studio, the rest of the configuration
// still comes from the configuration file and the environment variables.
// Changes to the Studio configuration require restarting the relay proxy.
type Studio struct {
	// Headers (optional) are sent with every request, for example an Authorization header.
	Headers map[string][]string `koanf:"headers" mapstructure:"headers"`
	// URL of the relay proxy configuration served by Studio, only its flagSets key is used.
	URL string `koanf:"url" mapstructure:"url"`
	// CacheFile (optional) keeps the last valid document, it is used when the URL is unreachable.
	CacheFile string `koanf:"cachefile" mapstructure:"cacheFile"`
	// PollingInterval (optional) is the time in milliseconds between two checks of the URL.
	// Default: 60000
	PollingInterval int `koanf:"pollinginterval" mapstructure:"pollingInterval"`
}

// parser returns the parser matching the extension of the URL path.
func (s Studio) parser() koanf.Parser {
	path, _, _ := strings.Cut(s.URL, "?") // pre-signed URLs have a query string after the extension

	return selectParserForFile(path)
}

// provider returns a koanf provider polling the URL.
func (s Studio) provider(parser koanf.Parser, log *slog.Logger) *httpprovider.HTTP {
	return httpprovider.Provider(s.URL,
		httpprovider.WithHeaders(s.Headers),
		httpprovider.WithPollInterval(time.Duration(s.PollingInterval)*time.Millisecond),
		httpprovider.WithCache(s.CacheFile),
		// Reject invalid flagSets before accepting their ETag or replacing the cache.
		httpprovider.WithValidate(func(content []byte) error {
			return validateStudioFlagSets(content, parser)
		}),
		httpprovider.WithOnFallback(func(fetchErr error, cachedAt time.Time) {
			log.LogAttrs(context.Background(), slog.LevelWarn, "studio unreachable, using the cached flagSets",
				cmdlog.Err(fetchErr), slog.Time("cachedAt", cachedAt))
		}),
	)
}

func validateStudioFlagSets(content []byte, parser koanf.Parser) error {
	values, err := parser.Unmarshal(content)
	if err != nil {
		return err
	}
	koanfLoader := koanf.New(".")
	loadErr := koanfLoader.Load(confmap.Provider(values, "."), nil)
	if loadErr != nil {
		return loadErr
	}
	var studioConf struct {
		FlagSets []FlagSet `koanf:"flagsets"`
	}
	unmarshalErr := koanfLoader.Unmarshal("", &studioConf)
	if unmarshalErr != nil {
		return unmarshalErr
	}

	return (&Config{FlagSets: studioConf.FlagSets}).validateFlagSets()
}
