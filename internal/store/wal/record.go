// Package wal is the write-ahead log. Every write lands here before the memtable.
package wal

import (
	"encoding/binary"
	"hash/crc32"
)

// RecordType is a put or a delete.
type RecordType uint8

const (
	// RecordPut sets a key.
	RecordPut RecordType = 1
	// RecordDelete deletes a key; Value is empty.
	RecordDelete RecordType = 2
)

// Record is one write in the log.
type Record struct {
	Seq   uint64
	Type  RecordType
	Key   []byte
	Value []byte
}

// castagnoli is the CRC-32C table for checksums.
var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// Header sizes in bytes.
const (
	crcSize    = 4
	lengthSize = 4
	seqSize    = 8
	typeSize   = 1
	keyLenSize = 4
	valLenSize = 4

	// bodyFixedSize is seq + type + keyLen + valLen.
	bodyFixedSize = seqSize + typeSize + keyLenSize + valLenSize
)

// encode writes r as:
//
//	[crc32 uint32][length uint32][seq uint64][type uint8][keyLen uint32][key][valLen uint32][value]
//
// The CRC covers everything after itself.
func encode(r Record) []byte {
	bodyLen := bodyFixedSize + len(r.Key) + len(r.Value)

	buf := make([]byte, crcSize+lengthSize+bodyLen)

	// Write length and body after the CRC.
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

// decodeBody parses a checksummed body. It returns false if the lengths don't fit.
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
