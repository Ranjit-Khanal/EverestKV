package resp

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strconv"
)

// Parser reads RESP2 values from an io.Reader.
type Parser struct {
	r *bufio.Reader
}

// NewParser returns a parser that reads from r.
func NewParser(r io.Reader) *Parser {
	return &Parser{r: bufio.NewReader(r)}
}

// Parse reads one complete RESP2 value.
func (p *Parser) Parse() (Value, error) {
	prefix, err := p.r.ReadByte()
	if err != nil {
		return Value{}, err
	}
	switch Type(prefix) {
	case TypeSimpleString, TypeError, TypeInteger:
		line, err := p.readLine()
		if err != nil {
			return Value{}, err
		}
		v := Value{Type: Type(prefix), Str: line}
		if Type(prefix) == TypeInteger {
			n, err := strconv.ParseInt(line, 10, 64)
			if err != nil {
				return Value{}, fmt.Errorf("resp: invalid integer %q: %w", line, err)
			}
			v.Int = n
		}
		return v, nil
	case TypeBulkString:
		return p.parseBulk()
	case TypeArray:
		return p.parseArray()
	default:
		return Value{}, fmt.Errorf("%w: %q", ErrInvalidPrefix, prefix)
	}
}

func (p *Parser) parseBulk() (Value, error) {
	n, err := p.readLength()
	if err != nil {
		return Value{}, err
	}
	if n < 0 {
		return Value{Type: TypeBulkString, Bulk: nil}, nil
	}
	data := make([]byte, n+2)
	if _, err := io.ReadFull(p.r, data); err != nil {
		return Value{}, err
	}
	if data[n] != '\r' || data[n+1] != '\n' {
		return Value{}, ErrInvalidLength
	}
	return Value{Type: TypeBulkString, Bulk: data[:n]}, nil
}

func (p *Parser) parseArray() (Value, error) {
	n, err := p.readLength()
	if err != nil {
		return Value{}, err
	}
	if n < 0 {
		return Value{Type: TypeArray, Array: nil}, nil
	}
	items := make([]Value, n)
	for i := int64(0); i < n; i++ {
		item, err := p.Parse()
		if err != nil {
			return Value{}, err
		}
		items[i] = item
	}
	return Value{Type: TypeArray, Array: items}, nil
}

func (p *Parser) readLength() (int64, error) {
	line, err := p.readLine()
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(line, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("resp: invalid length %q: %w", line, err)
	}
	return n, nil
}

func (p *Parser) readLine() (string, error) {
	line, err := p.r.ReadString('\n')
	if err != nil {
		return "", err
	}
	if len(line) < 2 || line[len(line)-2] != '\r' {
		return "", ErrInvalidLength
	}
	return line[:len(line)-2], nil
}

// ParseCommand parses one client command (a top-level array).
func ParseCommand(r io.Reader) (cmd string, args []string, err error) {
	v, err := NewParser(r).Parse()
	if err != nil {
		return "", nil, err
	}
	return v.Command()
}

// Decode is a convenience for parsing a single value from raw bytes.
func Decode(data []byte) (Value, error) {
	return NewParser(bytes.NewReader(data)).Parse()
}
