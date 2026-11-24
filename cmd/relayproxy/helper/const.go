package helper

// XAPIKeyHeader defines the header name for passing API keys.
// This is not a credential itself, but a standard HTTP header key.
const XAPIKeyHeader = "X-Api-Key" //nolint:gosec // G101: the name of a header, not a credential

// Names and values of the headers the relay proxy reads or writes.
const (
	ContentTypeHeader    = "Content-Type"
	HeaderContentType    = ContentTypeHeader
	AuthorizationHeader  = "Authorization"
	BearerPrefix         = "Bearer "
	ContentTypeValueJSON = "application/json"
	MIMEApplicationJSON  = ContentTypeValueJSON
)
