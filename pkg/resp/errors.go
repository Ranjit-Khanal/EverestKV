package resp

import "errors"

var (
	ErrInvalidPrefix = errors.New("resp: invalid type prefix")
	ErrInvalidLength = errors.New("resp: invalid length")
	ErrNotArray      = errors.New("resp: value is not an array")
	ErrNotBulkString = errors.New("resp: value is not a bulk string")
	ErrEmptyArray    = errors.New("resp: empty array")
	ErrNilBulk       = errors.New("resp: nil bulk string in command")
)
