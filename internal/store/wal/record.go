// Package wal implements the write-ahead commit log: an append-only,
// segmented, crash-safe record stream that the LSM engine appends to before
// applying a write to the memtable.
package wal

import (
	"encoding/binary"
	"hash/crc32"
)

// RecordType distinguishes a value write from a tombstone.
type RecordType uint8

const (
	// RecordPut is a normal key/value write.
	RecordPut RecordType = 1
	// RecordDelete is a tombstone; Value is always empty.
	RecordDelete RecordType = 2
)

// Record is a single logical write in the log.
type Record struct {
	Seq   uint64
	Type  RecordType
	Key   []byte
	Value []byte
}

// castagnoli is the CRC-32C table used for record checksums.
var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// header sizes, in bytes.
const (
	crcSize    = 4
	lengthSize = 4
	seqSize    = 8
	typeSize   = 1
	keyLenSize = 4
	valLenSize = 4

	// bodyFixedSize is the size of the fixed-width fields between length and
	// the variable-length key/value payloads: seq + type + keyLen + valLen.
	bodyFixedSize = seqSize + typeSize + keyLenSize + valLenSize
)

// encode serializes r as:
//
//	[crc32 uint32][length uint32][seq uint64][type uint8][keyLen uint32][key][valLen uint32][value]
//
// crc32 (Castagnoli) covers everything after the crc field itself, i.e.
// length through value.
func encode(r Record) []byte {
	bodyLen := bodyFixedSize + len(r.Key) + len(r.Value)

	buf := make([]byte, crcSize+lengthSize+bodyLen)

	// length + body, in place, starting after the crc field.
	rest := buf[crcSize:]
	binary.LittleEndian.PutUint32(rest[:lengthSize], uint32(bodyLen))

	body := rest[lengthSize:]
	off := 0
	binary.LittleEndian.PutUint64(body[off:], r.Seq)
	off += seqSize
	body[off] = byte(r.Type)
	off += typeSize
	binary.LittleEndian.PutUint32(body[off:], uint32(len(r.Key)))
	off += keyLenSize
	off += copy(body[off:], r.Key)
	binary.LittleEndian.PutUint32(body[off:], uint32(len(r.Value)))
	off += valLenSize
	copy(body[off:], r.Value)

	crc := crc32.Checksum(rest, castagnoli)
	binary.LittleEndian.PutUint32(buf[:crcSize], crc)

	return buf
}

// decodeBody parses the seq/type/keyLen/key/valLen/value fields out of a
// buffer already verified against its checksum. It returns false if the
// encoded lengths don't fit the buffer, which indicates corruption that
// happened to still pass the checksum (defensive; should not normally
// happen).
func decodeBody(body []byte) (Record, bool) {
	if len(body) < bodyFixedSize {
		return Record{}, false
	}

	off := 0
	seq := binary.LittleEndian.Uint64(body[off:])
	off += seqSize
	typ := RecordType(body[off])
	off += typeSize
	keyLen := binary.LittleEndian.Uint32(body[off:])
	off += keyLenSize

	if uint64(off)+uint64(keyLen)+valLenSize > uint64(len(body)) {
		return Record{}, false
	}
	key := body[off : off+int(keyLen)]
	off += int(keyLen)

	valLen := binary.LittleEndian.Uint32(body[off:])
	off += valLenSize

	if uint64(off)+uint64(valLen) != uint64(len(body)) {
		return Record{}, false
	}
	value := body[off : off+int(valLen)]

	return Record{Seq: seq, Type: typ, Key: key, Value: value}, true
}
