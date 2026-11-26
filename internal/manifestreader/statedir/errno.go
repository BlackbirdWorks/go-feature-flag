package statedir

import "syscall"

// The errors of the system that the writer tells apart (syscall errors are the same type on every platform).
var (
	errNoSpace  error = syscall.ENOSPC
	errReadOnly error = syscall.EROFS
)
