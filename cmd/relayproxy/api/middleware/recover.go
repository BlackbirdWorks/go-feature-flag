package middleware

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	cmdlog "github.com/blackbirdworks/semaphore/cmdhelpers/log"
)

// stackSize is the size of the buffer that receives the stack of a panic.
const stackSize = 4 << 10

// PanicStackError is the error of a request whose handler panicked: the value of the panic and the stack.
type PanicStackError struct {
	Err   error
	Stack []byte
}

// Error implements the error interface.
func (e *PanicStackError) Error() string {
	return fmt.Sprintf("[PANIC RECOVER] %s %s", e.Err.Error(), e.Stack)
}

// Unwrap returns the value of the panic.
func (e *PanicStackError) Unwrap() error { return e.Err }

// Recover turns a panic of a handler into a 500, and logs it with the error level. The stack is logged. At the
// debug level (exposeError) the client gets it too, like any other error. [http.ErrAbortHandler] goes on.
func Recover(logger *slog.Logger, exposeError bool) Func {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				p := recover()
				if p == nil {
					return
				}
				if errors.Is(asError(p), http.ErrAbortHandler) {
					panic(p)
				}
				err, ok := p.(error)
				if !ok {
					err = fmt.Errorf("%v", p) //nolint:err113 // the value of a panic has no type to wrap
				}
				stack := make([]byte, stackSize)
				stack = stack[:runtime.Stack(stack, true)]
				panicErr := &PanicStackError{Stack: stack, Err: err}

				logger.LogAttrs(r.Context(), slog.LevelError, "panic recovered",
					cmdlog.Err(err),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.String("stack", string(stack)),
				)
				helper.WriteError(w, r, panicErr, exposeError)
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func asError(v any) error {
	err, _ := v.(error)

	return err
}
