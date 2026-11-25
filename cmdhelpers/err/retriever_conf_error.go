package err

import (
	"fmt"
	"regexp"
	"strings"
)

var camelToKebabRegex = regexp.MustCompile("([a-z0-9])([A-Z])")

// RetrieverConfError reports an invalid retriever configuration.
type RetrieverConfError struct {
	property string
	kind     string
}

// NewRetrieverConfError returns an error for an invalid property of a retriever kind.
func NewRetrieverConfError(property, kind string) *RetrieverConfError {
	return &RetrieverConfError{
		property: property,
		kind:     kind,
	}
}

func (e *RetrieverConfError) Error() string {
	return fmt.Sprintf("invalid retriever: no \"%s\" property found for kind \"%s\"", e.property, e.kind)
}

// CliErrorMessage returns the message shown to the command line user.
func (e *RetrieverConfError) CliErrorMessage() string {
	kebab := camelToKebabRegex.ReplaceAllString(e.property, "${1}-${2}")

	return fmt.Sprintf("invalid retriever: no \"%s\" property found for kind \"%s\"", strings.ToLower(kebab), e.kind)
}
