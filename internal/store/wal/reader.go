package wal

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
)

// Reader reads records from one segment in order.
type Reader struct {
	f   *os.File
	own bool
}

// OpenSegmentReader opens segment in dir for reading.
func OpenSegmentReader(dir string, segment uint64) (*Reader, error) {
	f, err := os.Open(SegmentPath(dir, segment))
	if err != nil {
		return nil, fmt.Errorf("wal: open segment %d for read: %w", segment, err)
	}
	return &Reader{f: f, own: true}, nil
}

// Close closes the file if this Reader opened it.
func (r *Reader) Close() error {
	if !r.own {
		return nil
	}
	return r.f.Close()
}

// Next returns the next record. A torn or corrupt tail after a crash also
// returns io.EOF, since that is expected.
func (r *Reader) Next() (Record, error) {
	var crcBuf [crcSize]byte
	if _, err := io.ReadFull(r.f, crcBuf[:]); err != nil {
		return Record{}, io.EOF
	}
	wantCRC := binary.LittleEndian.Uint32(crcBuf[:])

	var lenBuf [lengthSize]byte
	if _, err := io.ReadFull(r.f, lenBuf[:]); err != nil {
		return Record{}, io.EOF
	}
	bodyLen := binary.LittleEndian.Uint32(lenBuf[:])

	body := make([]byte, bodyLen)
	if _, err := io.ReadFull(r.f, body); err != nil {
		return Record{}, io.EOF
	}

	crcInput := make([]byte, 0, lengthSize+len(body))
	crcInput = append(crcInput, lenBuf[:]...)
	crcInput = append(crcInput, body...)
	if crc32.Checksum(crcInput, castagnoli) != wantCRC {
		return Record{}, io.EOF
	}

	rec, ok := decodeBody(body)
	if !ok {
		return Record{}, io.EOF
	}
	return rec, nil
}
