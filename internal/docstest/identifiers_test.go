package docstest_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/corehttp"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

const repoRoot = "../.."

// identifierPages are the pages that quote metrics, fields, codes and log messages of the code.
func identifierPages() []string {
	return []string{
		docsRoot + "/relay-proxy/runbook.md",
		docsRoot + "/relay-proxy/security.md",
		docsRoot + "/concepts/reload-semantics.md",
		docsRoot + "/concepts/manifest-and-documents.md",
		docsRoot + "/concepts/request-budget.md",
		docsRoot + "/go-module/document-versioning.md",
	}
}

var (
	codeSpan   = regexp.MustCompile("`([^`\n]+)`")
	closedCode = regexp.MustCompile(`^[a-z]+(-[a-z]+)+$`)
	jsonTag    = regexp.MustCompile(`json:"([A-Za-z0-9_]+)[,"]`)
)

// sourceIndex is the names of the json tags and the text of the Go files of the program (no tests).
type sourceIndex struct {
	tags map[string]bool
	text string
}

func loadSources(t *testing.T) *sourceIndex {
	t.Helper()

	var text strings.Builder

	index := &sourceIndex{tags: map[string]bool{}}

	err := filepath.WalkDir(repoRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.IsDir() {
			switch entry.Name() {
			case "node_modules", ".git", "vendor", "docs":
				return filepath.SkipDir
			}

			return nil
		}

		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}

		text.Write(raw)

		for _, match := range jsonTag.FindAllSubmatch(raw, -1) {
			index.tags[string(match[1])] = true
		}

		return nil
	})
	require.NoError(t, err)

	index.text = text.String()
	require.NotEmpty(t, index.text)

	return index
}

// namesOnly is a registry that records the names of the metrics that are registered on it.
type namesOnly struct{ names map[string]bool }

var fqName = regexp.MustCompile(`fqName: "([^"]+)"`)

func (n *namesOnly) Register(c prom.Collector) error {
	descs := make(chan *prom.Desc, 64)

	go func() {
		c.Describe(descs)
		close(descs)
	}()

	for desc := range descs {
		if match := fqName.FindStringSubmatch(desc.String()); match != nil {
			n.names[match[1]] = true
		}
	}

	return nil
}

func (n *namesOnly) MustRegister(cs ...prom.Collector) {
	for _, c := range cs {
		_ = n.Register(c)
	}
}

func (*namesOnly) Unregister(prom.Collector) bool { return false }

// metricNames returns the names of the metrics that the relay registers for the control plane: the reader, the S3
// requests and the status of the configuration.
func metricNames(t *testing.T) map[string]bool {
	t.Helper()

	reg := &namesOnly{names: map[string]bool{}}

	_, err := metric.NewManifestReader(reg, "docs", func() manifestreader.Status { return manifestreader.Status{} })
	require.NoError(t, err)

	_, err = metric.NewS3Requests(reg)
	require.NoError(t, err)

	for _, name := range corehttp.MetricNames() {
		reg.names[name] = true
	}

	require.NotEmpty(t, reg.names)

	return reg.names
}

// Every identifier that a page quotes exists in the code: a metric (`semaphore_...`), a field of `/info` (`config...`),
// a log message (a quoted text) and a closed code (words joined by hyphens).
func TestQuotedIdentifiersExistInTheCode(t *testing.T) {
	t.Parallel()

	src := loadSources(t)
	metrics := metricNames(t)

	var missing []string

	for _, page := range identifierPages() {
		raw, err := os.ReadFile(page)
		require.NoError(t, err)

		checked := 0

		for _, match := range codeSpan.FindAllStringSubmatch(string(raw), -1) {
			token := match[1]

			var found bool

			switch {
			case strings.HasPrefix(token, "semaphore_"):
				name, _, _ := strings.Cut(token, "{")
				found = metrics[name]
			case strings.HasPrefix(token, "config."):
				found = true

				for segment := range strings.SplitSeq(strings.ReplaceAll(token, "[]", ""), ".") {
					found = found && src.tags[segment]
				}
			case strings.HasPrefix(token, `"`) && strings.HasSuffix(token, `"`):
				found = strings.Contains(src.text, strings.Trim(token, `"`))
			case closedCode.MatchString(token) && !strings.HasPrefix(token, "semaphore-"):
				found = strings.Contains(src.text, `"`+token+`"`)
			default:
				continue
			}

			checked++

			if !found {
				missing = append(missing, page+": "+token)
			}
		}

		assert.Positive(t, checked, "%s quotes identifiers of the code", page)
	}

	assert.Empty(t, missing, "identifiers that the code does not have")
}

// The matcher classifies a token as the page convention says.
func TestIdentifierConventions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		token string
		want  bool
	}{
		{token: "checksum-mismatch", want: true},
		{token: "semaphore-cli", want: true},
		{token: "Plain", want: false},
		{token: "two words", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.token, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, closedCode.MatchString(tt.token))
		})
	}
}
