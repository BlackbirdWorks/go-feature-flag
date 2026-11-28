package segmentadmin

import (
	"bufio"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// gzipMagic is the start of a gzip stream.
const gzipMagic = "\x1f\x8b"

// ObjectStream is the ids of the objects of a source.
type ObjectStream struct {
	reader      *Reader
	body        *objectBody
	verify      func() error
	fingerprint string
}

var _ Source = (*ObjectStream)(nil)

// Fingerprint is the fingerprint of the listing that the stream reads.
func (s *ObjectStream) Fingerprint() string { return s.fingerprint }

// Objects is how many objects the stream reads.
func (s *ObjectStream) Objects() int { return len(s.body.objects) }

// Next implements [Source]. At the end it checks that no object was added, replaced or removed meanwhile.
func (s *ObjectStream) Next() (Record, error) {
	rec, err := s.reader.Next()
	if errors.Is(err, io.EOF) {
		verr := s.verify()
		if verr != nil {
			return Record{}, fmt.Errorf("check the source: %w", verr)
		}
	}

	return rec, err
}

// Close closes the object that is open.
func (s *ObjectStream) Close() error { return s.body.close() }

// objectBody reads the objects one after the other as one stream.
type objectBody struct {
	open    func(ObjectInfo) (Object, error)
	current io.Reader
	closers []io.Closer
	objects []ObjectInfo
	next    int
	count   byteCount
	want    int64
	last    byte
}

func (b *objectBody) Read(buf []byte) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}

	for {
		err := b.advance()
		if err != nil {
			return 0, err
		}

		got, err := b.current.Read(buf)
		if got > 0 {
			b.last = buf[got-1]
		}

		if got > 0 || err != nil && !errors.Is(err, io.EOF) {
			return got, wrapRead(err)
		}

		if err == nil {
			continue
		}

		err = b.finish()
		if err != nil {
			return 0, err
		}

		if b.last != '\n' && b.last != 0 {
			// the line break keeps the last line of an object from joining the first of the next
			buf[0], b.last = '\n', 0

			return 1, nil
		}
	}
}

// advance opens the next object when none is open, and is [io.EOF] when there is none left.
func (b *objectBody) advance() error {
	if b.current != nil {
		return nil
	}

	if b.next >= len(b.objects) {
		return io.EOF
	}

	return b.start(b.objects[b.next])
}

// wrapRead names the failure of a read of an object; the end of the object is not one.
func wrapRead(err error) error {
	if err == nil || errors.Is(err, io.EOF) {
		return nil
	}

	return fmt.Errorf("read the source object: %w", err)
}

// start opens an object and puts its decompression in front of it when it is gzip.
func (b *objectBody) start(info ObjectInfo) error {
	obj, err := b.open(info)
	if err != nil {
		return fmt.Errorf("read the source object: %w", err)
	}

	b.closers = append(b.closers, obj.Body)
	b.count, b.want, b.last = 0, obj.Size, 0
	b.current = io.TeeReader(obj.Body, &b.count)

	if !obj.Gzip && !strings.HasSuffix(info.Key, ".gz") {
		return nil
	}

	plain := bufio.NewReader(b.current)

	magic, _ := plain.Peek(len(gzipMagic)) // a transport may have decompressed it already: then it is plain
	if string(magic) != gzipMagic {
		b.current = plain

		return nil
	}

	zip, err := gzip.NewReader(plain)
	if err != nil {
		return fmt.Errorf("read the source object: %w", err)
	}

	b.closers = append(b.closers, zip)
	b.current = zip

	return nil
}

// finish ends an object: it must have given every byte that the server announced.
func (b *objectBody) finish() error {
	b.current = nil
	b.next++

	closeErr := b.close()
	if closeErr != nil {
		return closeErr
	}

	if b.want > 0 && int64(b.count) != b.want {
		return fmt.Errorf("read the source object: %w", io.ErrUnexpectedEOF)
	}

	return nil
}

func (b *objectBody) close() error {
	var errs []error

	for _, closer := range slices.Backward(b.closers) {
		errs = append(errs, closer.Close())
	}

	b.closers = nil

	return errors.Join(errs...)
}

// byteCount counts the bytes that were written to it.
type byteCount int64

func (c *byteCount) Write(data []byte) (int, error) {
	*c += byteCount(len(data))

	return len(data), nil
}
