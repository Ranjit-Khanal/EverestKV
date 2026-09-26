package resp

import "errors"

// Errors returned by Parser and Value when input is not valid RESP2 or
// not shaped like a client command.
var (
	ErrInvalidPrefix = errors.New("resp: invalid type prefix")
	ErrInvalidLength = errors.New("resp: invalid length")
	ErrNotArray      = errors.New("resp: value is not an array")
	ErrNotBulkString = errors.New("resp: value is not a bulk string")
	ErrEmptyArray    = errors.New("resp: empty array")
	ErrNilBulk       = errors.New("resp: nil bulk string in command")
)
