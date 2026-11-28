//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly)

package filepublish

import (
	"fmt"
	"os"
	"runtime"
)

// lock is not available: there is no advisory lock without a dependency, and a lock file that survives a crash
// would block every later publish.
func lock(_ *os.Root) (func(), error) {
	return nil, fmt.Errorf("%w: publishing to a file store on %s", ErrUnsupported, runtime.GOOS)
}
