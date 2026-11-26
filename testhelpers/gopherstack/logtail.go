package gopherstack

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

// LogTail is the end of a log, with what could not be kept.
type LogTail struct {
	// ReadErr is the error that ended the read before the end of the log, nil when the log was read to its end.
	ReadErr error
	// Lines are the last lines read, in order. A line longer than the cap is cut and ends with a marker.
	Lines []string
	// MaxLine is the cap of a line, in bytes.
	MaxLine int
	// CutLines counts the lines that were longer than MaxLine.
	CutLines int
	// Want is the number of lines asked for.
	Want int
}

// Partial reports that the text is not the plain end of the log: the read failed or a line was cut.
func (l *LogTail) Partial() bool { return l.ReadErr != nil || l.CutLines > 0 }

// String is the text to print. A partial text says so and why, and is not called "the last N lines" when the read
// failed, because then the lines are the last ones that were read, not the end of the log.
func (l *LogTail) String() string {
	var head strings.Builder

	switch {
	case l.ReadErr != nil:
		fmt.Fprintf(&head, "PARTIAL log of the emulator: the read failed (%v); these are the last %d lines read, "+
			"not the end of the log", l.ReadErr, len(l.Lines))
	default:
		fmt.Fprintf(&head, "last %d lines of the log of the emulator", len(l.Lines))
	}

	if l.CutLines > 0 {
		fmt.Fprintf(&head, " (PARTIAL: %d lines longer than %d bytes were cut)", l.CutLines, l.MaxLine)
	}

	return head.String() + ":\n" + strings.Join(l.Lines, "\n")
}

// TailLog reads r to its end (or its first error) and keeps the last want lines. A line longer than maxLine bytes is
// cut there and ends with a visible marker, and is counted. It cannot fail on a long line.
func TailLog(r io.Reader, want, maxLine int) *LogTail {
	tail := &LogTail{Want: want, MaxLine: maxLine, Lines: make([]string, 0, want)}
	reader := bufio.NewReaderSize(r, maxLine)

	for {
		line, cut, err := readLine(reader, maxLine)
		if line != "" || cut || err == nil {
			if len(tail.Lines) == want {
				tail.Lines = tail.Lines[1:]
			}

			if cut {
				tail.CutLines++
				line += fmt.Sprintf(" [line cut at %d bytes]", maxLine)
			}

			tail.Lines = append(tail.Lines, line)
		}

		if err != nil {
			if !errors.Is(err, io.EOF) {
				tail.ReadErr = err
			}

			return tail
		}
	}
}

// readLine reads one line without its newline: at most maxLine bytes of it, the rest of a longer line is dropped and
// cut is true. At the end of the input it returns what was read of the last line and the error.
func readLine(reader *bufio.Reader, maxLine int) (string, bool, error) {
	var (
		line strings.Builder
		cut  bool
	)

	for {
		part, isPrefix, err := reader.ReadLine()
		if room := maxLine - line.Len(); len(part) > room {
			part = part[:max(room, 0)]
			cut = true
		}

		line.Write(part)

		if err != nil {
			return line.String(), cut, err
		}

		if !isPrefix {
			return line.String(), cut, nil
		}
	}
}
