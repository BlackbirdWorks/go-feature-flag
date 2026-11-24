package log

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// Output keys.
const (
	// LevelKey is the key of the level of a record.
	LevelKey = "level"
	// TimeKey is the key of the time of a record, in seconds since the epoch.
	TimeKey = "ts"
	// CallerKey is the key of the "dir/file.go:line" that logged the record.
	CallerKey = "caller"
	// MessageKey is the key of the message of a record.
	MessageKey = "msg"
	// ErrorKey is the key of the attribute made by [Err].
	ErrorKey = "error"
)

// Format is an output format of the handler.
type Format int32

const (
	// FormatJSON writes one JSON object per line.
	FormatJSON Format = iota
	// FormatLogfmt writes one line of key=value pairs per record.
	FormatLogfmt
)

const (
	hexDigits     = "0123456789abcdef"
	initialBuffer = 512
	// maxPooledBuffer is the size above which a buffer is not given back to the pool.
	maxPooledBuffer = 16 << 10
	nanosPerSecond  = float64(time.Second)
	asciiControl    = 0x20
	base10          = 10
	floatBits       = 64
	// callerPathElements is the number of trailing path elements of the caller file that are logged.
	callerPathElements = 2
)

// core is what the handlers derived from each other (With, WithGroup) share.
type core struct {
	out    io.Writer
	pool   sync.Pool
	level  slog.LevelVar
	mu     sync.Mutex
	format atomic.Int32
}

func newCore(out io.Writer) *core {
	c := &core{out: out}
	c.pool.New = func() any {
		b := make([]byte, 0, initialBuffer)

		return &b
	}

	return c
}

// handler is a [slog.Handler] writing the records in the format of the former zap based logger: the JSON
// keys level, ts, caller and msg, then the attributes, or the logfmt pairs ts, level, caller, msg. It does not
// allocate for the records of the usual attribute kinds.
type handler struct {
	core   *core
	prefix string
	attrs  []slog.Attr
}

func (h *handler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.core.level.Level()
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	merged := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	merged = append(merged, h.attrs...)
	for _, a := range attrs {
		merged = append(merged, prefixAttr(h.prefix, a))
	}

	return &handler{core: h.core, prefix: h.prefix, attrs: merged}
}

// prefixAttr gives the key of an attribute added under a group its dotted name.
func prefixAttr(prefix string, a slog.Attr) slog.Attr {
	if prefix == "" {
		return a
	}
	a.Key = prefix + a.Key

	return a
}

func (h *handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}

	return &handler{core: h.core, prefix: h.prefix + name + ".", attrs: h.attrs}
}

func (h *handler) Handle(_ context.Context, r slog.Record) error {
	bp, _ := h.core.pool.Get().(*[]byte)
	buf := (*bp)[:0]
	isJSON := Format(h.core.format.Load()) == FormatJSON

	if isJSON {
		buf = append(buf, `{"level":"`...)
		buf = append(buf, levelName(r.Level)...)
		buf = append(buf, `","ts":`...)
		buf = appendSeconds(buf, r.Time)
	} else {
		buf = append(buf, "ts="...)
		buf = appendSeconds(buf, r.Time)
		buf = append(buf, " level="...)
		buf = append(buf, levelName(r.Level)...)
	}
	if r.PC != 0 {
		buf = appendCaller(buf, isJSON, r.PC)
	}
	buf = appendKey(buf, isJSON, MessageKey)
	buf = appendString(buf, isJSON, r.Message)

	for _, a := range h.attrs {
		buf = appendAttr(buf, isJSON, "", a)
	}
	r.Attrs(func(a slog.Attr) bool {
		buf = appendAttr(buf, isJSON, h.prefix, a)

		return true
	})

	if isJSON {
		buf = append(buf, '}')
	}
	buf = append(buf, '\n')

	h.core.mu.Lock()
	_, err := h.core.out.Write(buf)
	h.core.mu.Unlock()

	if cap(buf) <= maxPooledBuffer {
		*bp = buf
		h.core.pool.Put(bp)
	}

	return err //nolint:wrapcheck // the error of the writer is returned as is
}

func appendSeconds(buf []byte, t time.Time) []byte {
	return strconv.AppendFloat(buf, float64(t.UnixNano())/nanosPerSecond, 'f', -1, floatBits)
}

// appendCaller adds the "dir/file.go:line" of the program counter.
func appendCaller(buf []byte, isJSON bool, pc uintptr) []byte {
	fn := runtime.FuncForPC(pc)
	if fn == nil {
		return buf
	}
	file, line := fn.FileLine(pc)
	// keep the last 2 elements of the path.
	idx := lastIndexByte(file, '/')
	if idx >= 0 {
		for i := 1; i < callerPathElements; i++ {
			idx = lastIndexByte(file[:idx], '/')
			if idx < 0 {
				break
			}
		}
		file = file[idx+1:]
	}
	buf = appendKey(buf, isJSON, CallerKey)
	if isJSON {
		buf = append(buf, '"')
	}
	buf = appendEscaped(buf, file)
	buf = append(buf, ':')
	buf = strconv.AppendInt(buf, int64(line), base10)
	if isJSON {
		buf = append(buf, '"')
	}

	return buf
}

func lastIndexByte(s string, c byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == c {
			return i
		}
	}

	return -1
}

func appendKey(buf []byte, isJSON bool, key string) []byte {
	if isJSON {
		buf = append(buf, ',', '"')
		buf = appendEscaped(buf, key)

		return append(buf, '"', ':')
	}
	buf = append(buf, ' ')
	buf = appendEscaped(buf, key)

	return append(buf, '=')
}

func appendAttr(buf []byte, isJSON bool, prefix string, a slog.Attr) []byte {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return buf
	}
	if a.Value.Kind() == slog.KindGroup {
		group := a.Value.Group()
		if len(group) == 0 {
			return buf
		}
		if a.Key != "" {
			prefix += a.Key + "."
		}
		for _, ga := range group {
			buf = appendAttr(buf, isJSON, prefix, ga)
		}

		return buf
	}
	if prefix == "" {
		buf = appendKey(buf, isJSON, a.Key)
	} else {
		buf = appendKey(buf, isJSON, prefix+a.Key)
	}

	return appendValue(buf, isJSON, a.Value)
}

func appendValue(buf []byte, isJSON bool, v slog.Value) []byte {
	switch v.Kind() {
	case slog.KindString:
		return appendString(buf, isJSON, v.String())
	case slog.KindInt64:
		return strconv.AppendInt(buf, v.Int64(), base10)
	case slog.KindUint64:
		return strconv.AppendUint(buf, v.Uint64(), base10)
	case slog.KindFloat64:
		return appendFloat(buf, isJSON, v.Float64())
	case slog.KindBool:
		return strconv.AppendBool(buf, v.Bool())
	case slog.KindDuration:
		return strconv.AppendFloat(buf, float64(v.Duration())/nanosPerSecond, 'f', -1, floatBits)
	case slog.KindTime:
		return appendSeconds(buf, v.Time())
	case slog.KindAny, slog.KindGroup, slog.KindLogValuer:
		return appendAny(buf, isJSON, v.Any())
	default:
		return appendString(buf, isJSON, v.String())
	}
}

func appendFloat(buf []byte, isJSON bool, f float64) []byte {
	switch {
	case math.IsNaN(f):
		return appendString(buf, isJSON, "NaN")
	case math.IsInf(f, 1):
		return appendString(buf, isJSON, "+Inf")
	case math.IsInf(f, -1):
		return appendString(buf, isJSON, "-Inf")
	default:
		return strconv.AppendFloat(buf, f, 'f', -1, floatBits)
	}
}

// appendAny encodes the values that are not of a basic kind. An error is its message, and a slice of strings
// a list (comma separated in logfmt). Everything else goes through encoding/json (the encoded value is quoted in
// logfmt), which allocates: keep these values out of the hot paths.
func appendAny(buf []byte, isJSON bool, v any) []byte {
	switch x := v.(type) {
	case nil:
		return append(buf, "null"...)
	case error:
		return appendString(buf, isJSON, x.Error())
	case []byte:
		return appendString(buf, isJSON, string(x))
	case []string:
		return appendStrings(buf, isJSON, x)
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		return appendString(buf, isJSON, "unsupported value type")
	}
	if isJSON {
		return append(buf, encoded...)
	}

	return appendString(buf, false, string(encoded))
}

func appendStrings(buf []byte, isJSON bool, list []string) []byte {
	if !isJSON {
		// logfmt has no list, the elements are joined with commas, like the former encoder did.
		joined := make([]byte, 0, initialBuffer)
		for i, s := range list {
			if i > 0 {
				joined = append(joined, ',')
			}
			joined = append(joined, s...)
		}

		return appendString(buf, false, string(joined))
	}
	buf = append(buf, '[')
	for i, s := range list {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = appendString(buf, true, s)
	}

	return append(buf, ']')
}

func appendString(buf []byte, isJSON bool, s string) []byte {
	if isJSON {
		buf = append(buf, '"')
		buf = appendEscaped(buf, s)

		return append(buf, '"')
	}
	quote := false
	for _, r := range s {
		if r <= ' ' || r == '=' || r == '"' || r == utf8.RuneError {
			quote = true

			break
		}
	}
	if quote {
		buf = append(buf, '"')
	}
	buf = appendEscaped(buf, s)
	if quote {
		buf = append(buf, '"')
	}

	return buf
}

// appendEscaped adds s with the escapes of a JSON string, which are also the ones of a quoted logfmt value.
func appendEscaped(buf []byte, s string) []byte {
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			i++
			switch c {
			case '"', '\\':
				buf = append(buf, '\\', c)
			case '\n':
				buf = append(buf, '\\', 'n')
			case '\r':
				buf = append(buf, '\\', 'r')
			case '\t':
				buf = append(buf, '\\', 't')
			default:
				if c < asciiControl {
					buf = append(buf, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xf])
				} else {
					buf = append(buf, c)
				}
			}

			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			buf = append(buf, `�`...)
			i++

			continue
		}
		buf = append(buf, s[i:i+size]...)
		i += size
	}

	return buf
}
