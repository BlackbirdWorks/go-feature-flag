package segmentadmin

import (
	"bufio"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Format is how the input of an import is laid out.
type Format string

const (
	// FormatLines is one id for each line.
	FormatLines Format = "lines"
	// FormatCSV is one record for each line: the id is a column of it.
	FormatCSV Format = "csv"
)

// Reason is why a line was not taken as an id. The counts of the reasons are reported, never the lines.
type Reason string

const (
	// ReasonEmpty is a line (or column) with no id, or an id that is empty once normalised.
	ReasonEmpty Reason = "empty"
	// ReasonTooLong is a line longer than the limit, or an id longer than the longest id that the relay looks up.
	ReasonTooLong Reason = "tooLong"
	// ReasonInvalid is an id that is not valid UTF-8 under a normalising segment.
	ReasonInvalid Reason = "invalid"
	// ReasonColumn is a CSV record with no such column, or that is not valid CSV.
	ReasonColumn Reason = "column"
)

// Rejections counts the lines that were not an id, by reason.
type Rejections struct {
	// Empty counts the lines with no id.
	Empty int64
	// TooLong counts the lines or ids over the limit.
	TooLong int64
	// Invalid counts the ids that are not valid UTF-8 under a normalising segment.
	Invalid int64
	// Column counts the CSV records with no such column.
	Column int64
}

// Add counts one rejected line.
func (r *Rejections) Add(reason Reason) {
	switch reason {
	case ReasonEmpty:
		r.Empty++
	case ReasonTooLong:
		r.TooLong++
	case ReasonInvalid:
		r.Invalid++
	case ReasonColumn:
		r.Column++
	}
}

// DefaultMaxLineBytes is the longest line that is read when ReaderOptions.MaxLineBytes is zero.
const DefaultMaxLineBytes = 64 << 10

// minLineBuffer is the smallest buffer of the line reader.
const minLineBuffer = 16

// ReaderOptions set how an input is read.
type ReaderOptions struct {
	// Format is FormatLines (default) or FormatCSV.
	Format Format
	// Column is the 1-based column of the id of a CSV record. Zero means the first.
	Column int
	// MaxLineBytes bounds a line; a longer one is rejected, not buffered. Zero means [DefaultMaxLineBytes].
	MaxLineBytes int
}

// Record is one line of an input.
type Record struct {
	// ID is the id as written, not yet normalised. Empty when Reason is set.
	ID string
	// Reason is why the line holds no id, empty for an id.
	Reason Reason
	// Line is the 1-based number of the line.
	Line int
}

// Reader streams the ids of an input. It holds one line at a time. A source of an import (a file, standard input,
// later an object of a bucket) is anything that gives an [io.Reader].
type Reader struct {
	src    *bufio.Reader
	format Format
	column int
	line   int
}

// NewReader returns a Reader of src.
func NewReader(src io.Reader, opts ReaderOptions) (*Reader, error) {
	if opts.Format == "" {
		opts.Format = FormatLines
	}

	if opts.Format != FormatLines && opts.Format != FormatCSV {
		return nil, fmt.Errorf("%w: format must be lines or csv", ErrBadOptions)
	}

	if opts.Column < 0 {
		return nil, fmt.Errorf("%w: column must be 1 or more", ErrBadOptions)
	}

	if opts.MaxLineBytes < 0 {
		return nil, fmt.Errorf("%w: max line bytes must be positive", ErrBadOptions)
	}

	if opts.MaxLineBytes == 0 {
		opts.MaxLineBytes = DefaultMaxLineBytes
	}

	return &Reader{
		src: bufio.NewReaderSize(src, max(opts.MaxLineBytes, minLineBuffer)), format: opts.Format,
		column: max(opts.Column, 1),
	}, nil
}

// Next returns the next line of the input. A line is read whole or rejected: a long line is skipped to its end without
// being held. It returns [io.EOF] at the end of the input.
func (r *Reader) Next() (Record, error) {
	raw, tooLong, err := r.readLine()
	if err != nil {
		return Record{Line: r.line}, err
	}

	r.line++
	rec := Record{Line: r.line}

	switch {
	case tooLong:
		rec.Reason = ReasonTooLong
	case r.format == FormatCSV:
		rec.ID, rec.Reason = r.field(string(raw))
	case strings.TrimSpace(string(raw)) == "":
		rec.Reason = ReasonEmpty
	default:
		rec.ID = string(raw)
	}

	return rec, nil
}

// readLine reads one line without its end. A line that fills the buffer is skipped to its end.
func (r *Reader) readLine() ([]byte, bool, error) {
	line, prefix, err := r.src.ReadLine()
	if err != nil {
		return nil, false, fmt.Errorf("read the input: %w", err)
	}

	if !prefix {
		return line, false, nil
	}

	for prefix {
		_, prefix, err = r.src.ReadLine()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return nil, false, fmt.Errorf("read the input: %w", err)
		}
	}

	return nil, true, nil
}

// field takes the id out of a CSV line.
func (r *Reader) field(text string) (string, Reason) {
	if strings.TrimSpace(text) == "" {
		return "", ReasonEmpty
	}

	cr := csv.NewReader(strings.NewReader(text))
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true

	cols, err := cr.Read()
	if err != nil || len(cols) < r.column {
		return "", ReasonColumn
	}

	if strings.TrimSpace(cols[r.column-1]) == "" {
		return "", ReasonEmpty
	}

	return cols[r.column-1], ""
}
