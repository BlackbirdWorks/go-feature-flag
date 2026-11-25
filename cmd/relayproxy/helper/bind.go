package helper

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
)

// maxPooledBody is the largest request buffer that goes back to the pool: a bigger one would pin its memory.
const maxPooledBody = 64 << 10

//nolint:gochecknoglobals // a buffer pool is stateless
var bodyBuffers = sync.Pool{
	New: func() any { return new(bytes.Buffer) },
}

// IsJSON reports whether the content type is application/json, whatever its parameters.
func IsJSON(contentType string) bool {
	base, _, _ := strings.Cut(contentType, ";")

	return strings.TrimSpace(base) == MIMEApplicationJSON
}

// BindBody decodes the JSON body of the request in target. An empty body leaves target untouched, another
// content type than JSON is refused with 415, and a body that can not be read or decoded with 400.
func BindBody(r *http.Request, target any) error {
	if r.ContentLength == 0 {
		return nil
	}
	if !IsJSON(r.Header.Get(HeaderContentType)) {
		return NewHTTPError(http.StatusUnsupportedMediaType, http.StatusText(http.StatusUnsupportedMediaType))
	}

	buf, _ := bodyBuffers.Get().(*bytes.Buffer)
	buf.Reset()
	defer func() {
		if buf.Cap() <= maxPooledBody {
			bodyBuffers.Put(buf)
		}
	}()

	_, err := buf.ReadFrom(r.Body)
	if err != nil {
		return wrapBindError(err)
	}

	return DecodeJSON(buf.Bytes(), target)
}

// DecodeJSON decodes a JSON document in target. A failure is a 400.
func DecodeJSON(data []byte, target any) error {
	err := json.Unmarshal(data, target)
	if err != nil {
		return WrapHTTPError(http.StatusBadRequest, err)
	}

	return nil
}

// BindRawBody is BindBody for a body that was read already, and checked against its content type.
func BindRawBody(contentType string, raw []byte, target any) error {
	if len(raw) == 0 {
		return nil
	}
	if !IsJSON(contentType) {
		return NewHTTPError(http.StatusUnsupportedMediaType, http.StatusText(http.StatusUnsupportedMediaType))
	}

	return DecodeJSON(raw, target)
}

// wrapBindError keeps the status of an error that has one and makes any other failure to read the body a 400.
func wrapBindError(err error) error {
	var sc interface{ StatusCode() int }
	if errors.As(err, &sc) {
		return err
	}

	return WrapHTTPError(http.StatusBadRequest, err)
}
