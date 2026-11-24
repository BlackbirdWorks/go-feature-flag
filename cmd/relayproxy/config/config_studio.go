package config

import (
	"strings"
	"time"

	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/v2"
	"go.uber.org/zap"

	httpprovider "github.com/blackbirdworks/semaphore/internal/koanfhttpprovider"
)

// Studio holds the configuration to load the flagSets from Semaphore Studio.
// When URL is set, the flagSets only come from Studio, the rest of the configuration
// still comes from the configuration file and the environment variables.
// Changes to the Studio configuration require restarting the relay proxy.
type Studio struct {
	// Headers (optional) are sent with every request, for example an Authorization header.
	Headers map[string][]string `mapstructure:"headers" koanf:"headers"`
	// URL of the relay proxy configuration served by Studio, only its flagSets key is used.
	URL string `mapstructure:"url" koanf:"url"`
	// CacheFile (optional) keeps the last valid document, it is used when the URL is unreachable.
	CacheFile string `mapstructure:"cacheFile" koanf:"cachefile"`
	// PollingInterval (optional) is the time in milliseconds between two checks of the URL.
	// Default: 60000
	PollingInterval int `mapstructure:"pollingInterval" koanf:"pollinginterval"`
}

// parser returns the parser matching the extension of the URL path.
func (s Studio) parser() koanf.Parser {
	path, _, _ := strings.Cut(s.URL, "?") // pre-signed URLs have a query string after the extension

	return selectParserForFile(path)
}

// provider returns a koanf provider polling the URL.
func (s Studio) provider(parser koanf.Parser, log *zap.Logger) *httpprovider.HTTP {
	return httpprovider.Provider(s.URL,
		httpprovider.WithHeaders(s.Headers),
		httpprovider.WithPollInterval(time.Duration(s.PollingInterval)*time.Millisecond),
		httpprovider.WithCache(s.CacheFile),
		// Reject invalid flagSets before accepting their ETag or replacing the cache.
		httpprovider.WithValidate(func(content []byte) error {
			return validateStudioFlagSets(content, parser)
		}),
		httpprovider.WithOnFallback(func(fetchErr error, cachedAt time.Time) {
			log.Warn("studio unreachable, using the cached flagSets",
				zap.Error(fetchErr), zap.Time("cachedAt", cachedAt))
		}),
	)
}

func validateStudioFlagSets(content []byte, parser koanf.Parser) error {
	values, err := parser.Unmarshal(content)
	if err != nil {
		return err
	}
	k := koanf.New(".")
	if loadErr := k.Load(confmap.Provider(values, "."), nil); loadErr != nil {
		return loadErr
	}
	var studioConf struct {
		FlagSets []FlagSet `koanf:"flagsets"`
	}
	if unmarshalErr := k.Unmarshal("", &studioConf); unmarshalErr != nil {
		return unmarshalErr
	}

	return (&Config{FlagSets: studioConf.FlagSets}).validateFlagSets()
}
