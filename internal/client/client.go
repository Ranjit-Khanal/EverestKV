// Package client implements a small RESP2 client for talking to an
// EverestKV server. It is the single place that knows how to turn
// operations (PING, GET, SET, ...) into wire requests and replies back
// into Go values, so every frontend — the interactive CLI, the web
// dashboard, tests — drives the server through the exact same path.
package client

import (
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/Ranjit-Khanal/everestkv/pkg/resp"
)

// Client is a connection to an EverestKV server. It is safe for
// concurrent use: commands are serialized so replies can never be
// read out of order on the shared connection.
type Client struct {
	addr string

	mu     sync.Mutex
	conn   net.Conn
	parser *resp.Parser
	writer *resp.Writer
}

// Dial connects to an EverestKV server at addr (e.g. "localhost:6379").
func Dial(addr string) (*Client, error) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	return &Client{
		addr:   addr,
		conn:   conn,
		parser: resp.NewParser(conn),
		writer: resp.NewWriter(conn),
	}, nil
}

// Close closes the underlying connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.Close()
}

// Do sends args as a command array and returns the raw parsed reply.
// It is the primitive every higher-level helper and the console
// pass-through are built on.
func (c *Client) Do(args ...string) (resp.Value, error) {
	if len(args) == 0 {
		return resp.Value{}, fmt.Errorf("client: no command given")
	}

	items := make([]resp.Value, len(args))
	for i, a := range args {
		items[i] = resp.Value{Type: resp.TypeBulkString, Bulk: []byte(a)}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.writer.Write(resp.Value{Type: resp.TypeArray, Array: items}); err != nil {
		return resp.Value{}, err
	}
	return c.parser.Parse()
}

// Execute parses a plain command line the way the CLI's input box
// does (whitespace-separated fields) and runs it.
func (c *Client) Execute(line string) (resp.Value, error) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return resp.Value{}, fmt.Errorf("client: empty command")
	}
	return c.Do(fields...)
}

// Ping checks liveness against the server.
func (c *Client) Ping() (string, error) {
	v, err := c.Do("PING")
	if err != nil {
		return "", err
	}
	return replyString(v)
}

// Echo returns msg as echoed back by the server.
func (c *Client) Echo(msg string) (string, error) {
	v, err := c.Do("ECHO", msg)
	if err != nil {
		return "", err
	}
	return replyString(v)
}

// Get returns the value for key. found is false when the key does
// not exist (a RESP nil bulk string), not an error.
func (c *Client) Get(key string) (value string, found bool, err error) {
	v, err := c.Do("GET", key)
	if err != nil {
		return "", false, err
	}
	if v.Type == resp.TypeBulkString && v.Bulk == nil {
		return "", false, nil
	}
	s, err := replyString(v)
	if err != nil {
		return "", false, err
	}
	return s, true, nil
}

// Set stores value under key.
func (c *Client) Set(key, value string) error {
	v, err := c.Do("SET", key, value)
	if err != nil {
		return err
	}
	_, err = replyString(v)
	return err
}

// Keys returns every key currently stored (KEYS *).
func (c *Client) Keys() ([]string, error) {
	v, err := c.Do("KEYS", "*")
	if err != nil {
		return nil, err
	}
	if IsError(v) {
		return nil, fmt.Errorf("%s", v.Str)
	}
	if v.Type != resp.TypeArray {
		return nil, fmt.Errorf("client: unexpected reply type for KEYS")
	}
	keys := make([]string, len(v.Array))
	for i, item := range v.Array {
		s, err := item.BulkString()
		if err != nil {
			return nil, err
		}
		keys[i] = s
	}
	return keys, nil
}

// replyString normalizes a reply into a display string, surfacing
// RESP-level errors as Go errors.
func replyString(v resp.Value) (string, error) {
	switch v.Type {
	case resp.TypeSimpleString:
		return v.Str, nil
	case resp.TypeBulkString:
		return string(v.Bulk), nil
	case resp.TypeInteger:
		return fmt.Sprintf("%d", v.Int), nil
	case resp.TypeError:
		return "", fmt.Errorf("%s", v.Str)
	case resp.TypeArray:
		parts := make([]string, len(v.Array))
		for i, item := range v.Array {
			s, err := replyString(item)
			if err != nil {
				return "", err
			}
			parts[i] = s
		}
		return strings.Join(parts, "\n"), nil
	default:
		return "", fmt.Errorf("client: unexpected reply type %q", v.Type)
	}
}

// FormatReply renders a raw reply the way a terminal would print it,
// including type markers for nil/array replies. Used by the CLI and
// mirrored by the web console so both show identical output.
func FormatReply(v resp.Value) string {
	switch v.Type {
	case resp.TypeBulkString:
		if v.Bulk == nil {
			return "(nil)"
		}
		return string(v.Bulk)
	case resp.TypeArray:
		if v.Array == nil {
			return "(nil)"
		}
		if len(v.Array) == 0 {
			return "(empty array)"
		}
		parts := make([]string, len(v.Array))
		for i, item := range v.Array {
			parts[i] = FormatReply(item)
		}
		return strings.Join(parts, "\n")
	case resp.TypeError:
		return v.Str
	case resp.TypeInteger:
		return fmt.Sprintf("%d", v.Int)
	default:
		return v.Str
	}
}

// IsError reports whether v is a RESP error reply.
func IsError(v resp.Value) bool {
	return v.Type == resp.TypeError
}
