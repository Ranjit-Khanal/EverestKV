// Package resp encodes and decodes the RESP2 wire protocol used by Redis
// (https://redis.io/docs/latest/develop/reference/protocol-spec/). It
// supports the five RESP2 types — simple strings, errors, integers, bulk
// strings and arrays, including null bulk strings and null arrays — and
// knows nothing about commands or storage.
//
// Parser reads values from a stream; Writer writes them. Value.Command
// turns a client request (an array of bulk strings) into a command name
// and arguments. Inline (non-array) commands are not supported.
package resp

// Type is the first byte of a RESP2 value.
type Type byte

// RESP2 type prefixes.
const (
	TypeSimpleString Type = '+'
	TypeError        Type = '-'
	TypeInteger      Type = ':'
	TypeBulkString   Type = '$'
	TypeArray        Type = '*'
)

// Value is a parsed RESP2 value.
type Value struct {
	Type  Type
	Str   string
	Int   int64
	Bulk  []byte
	Array []Value
}

// Command returns the command name and arguments from a top-level array.
// The first bulk string is the command; the rest are arguments.
func (v Value) Command() (string, []string, error) {
	if v.Type != TypeArray {
		return "", nil, ErrNotArray
	}
	if len(v.Array) == 0 {
		return "", nil, ErrEmptyArray
	}
	cmd, err := v.Array[0].BulkString()
	if err != nil {
		return "", nil, err
	}
	args := make([]string, 0, len(v.Array)-1)
	for _, elem := range v.Array[1:] {
		s, err := elem.BulkString()
		if err != nil {
			return "", nil, err
		}
		args = append(args, s)
	}
	return cmd, args, nil
}

// BulkString returns the bulk string payload or an error if the type is wrong.
func (v Value) BulkString() (string, error) {
	if v.Type != TypeBulkString {
		return "", ErrNotBulkString
	}
	return string(v.Bulk), nil
}
