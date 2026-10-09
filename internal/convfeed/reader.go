// Package convfeed follows one Claude Code transcript file and keeps a
// conversation model of it (interface U1-6, spec §8.2): an open-file follower
// that feeds a ccnorm.Normalizer in byte order, a revision per turn and item,
// the size-capped window and the changes since a revision.
//
// Standard library, convmodel and ccnorm only (an import-boundary test keeps
// it so): the resolver, the cache and the HTTP / WebSocket layers that use it
// live elsewhere.
package convfeed

import (
	"bytes"
	"context"
	"errors"
	"io"
)

// File is what the follower reads: random access to the bytes and the size
// now. The resolver hands over an open descriptor (never a bare path).
type File interface {
	io.ReaderAt
	Size() (int64, error)
}

const (
	chunkBytes = 2 << 20 // one read from the file
	maxLine    = 8 << 20 // a longer line is never buffered; the normalizer skips it
)

// lineSink takes what the reader finds. feed gets a complete line (valid only
// during the call); skip gets the offset and length of a line over maxLine,
// which was not read into memory.
type lineSink struct {
	feed func(offset int64, line []byte) error
	skip func(offset, length int64) error
}

// readLines reads the complete lines in [from, size) in chunks of at most
// chunkBytes, checking ctx between chunks, and returns the offset of the first
// byte not consumed: the start of an unterminated tail (a writer mid-line),
// which waits for the next call. A line over maxLine is counted to its newline
// without being held whole.
func readLines(ctx context.Context, f File, from, size int64, sink lineSink) (int64, error) {
	buf := make([]byte, chunkBytes)
	lineOff := from // where the line being read starts
	readPos := from
	var carry []byte // the part of the current line read in earlier chunks
	long := false    // the current line is past chunkBytes: only its length is tracked
	var longLen int64

	for readPos < size {
		if err := ctx.Err(); err != nil {
			return lineOff, err
		}
		want := int64(len(buf))
		if size-readPos < want {
			want = size - readPos
		}
		n, err := f.ReadAt(buf[:want], readPos)
		if n == 0 && err != nil {
			if errors.Is(err, io.EOF) {
				break // the file is shorter than its size said: the rest waits
			}
			return lineOff, err
		}
		data := buf[:n]
		readPos += int64(n)

		for len(data) > 0 {
			idx := bytes.IndexByte(data, '\n')
			if long {
				if idx < 0 {
					longLen += int64(len(data))
					break
				}
				longLen += int64(idx)
				data = data[idx+1:]
				if err := finishLong(f, lineOff, longLen, sink); err != nil {
					return lineOff, err
				}
				lineOff += longLen + 1
				long, longLen = false, 0
				continue
			}
			if idx < 0 { // no newline in what is left of this chunk
				if len(carry)+len(data) > chunkBytes {
					long, longLen = true, int64(len(carry)+len(data))
					carry = carry[:0]
				} else {
					carry = append(carry, data...)
				}
				break
			}
			var line []byte
			if len(carry) > 0 {
				carry = append(carry, data[:idx]...)
				line = carry
			} else {
				line = data[:idx]
			}
			if err := sink.feed(lineOff, line); err != nil {
				return lineOff, err
			}
			lineOff += int64(len(line)) + 1
			carry = carry[:0]
			data = data[idx+1:]
		}
	}
	return lineOff, nil
}

// finishLong settles a line that outgrew a chunk: up to maxLine it is read
// back whole (its exact size, once) and fed; past it, it is skipped unread.
func finishLong(f File, off, length int64, sink lineSink) error {
	if length > maxLine {
		return sink.skip(off, length)
	}
	line := make([]byte, length)
	if _, err := f.ReadAt(line, off); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return sink.feed(off, line)
}
