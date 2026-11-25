package protocol

// HTTP header names.
const (
	// HeaderAuthorization is the name of the header carrying the credentials of a request.
	HeaderAuthorization = "Authorization"
	// HeaderContentType is the name of the header carrying the media type of a body.
	HeaderContentType = "Content-Type"
	// HeaderETag is the name of the header carrying the entity tag of a response.
	HeaderETag = "ETag"
	// HeaderIfNoneMatch is the name of the header carrying the entity tag a client already holds.
	HeaderIfNoneMatch = "If-None-Match"
	// HeaderOrigin is the name of the header carrying the origin of a cross-origin request.
	HeaderOrigin = "Origin"
)

const (
	// BearerPrefix is the scheme prefix of an Authorization header carrying a bearer token.
	BearerPrefix = "Bearer "
	// MIMEApplicationJSON is the media type of a JSON body.
	MIMEApplicationJSON = "application/json"
)
