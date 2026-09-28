// Package client implements a small HTTP client for talking to an
// EverestKV server. It is the single place that knows how to turn
// operations (PING, GET, SET, ...) into HTTP requests and responses back
// into Go values, so every frontend — the interactive CLI, the web
// dashboard, tests — drives the server through the exact same path.
package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client talks to an EverestKV server. It is safe for concurrent use.
type Client struct {
	base string
	http *http.Client
}

// APIError is an error response from the server.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("server: %s (HTTP %d)", e.Message, e.Status)
}

// CommandError is a command line Execute could not run: an unknown
// command or the wrong number of arguments.
type CommandError struct {
	Message string
}

func (e *CommandError) Error() string { return e.Message }

// New returns a client for the server at addr, either "host:port" or a
// full "http://host:port" base URL. It does not contact the server; call
// Ping to check it is reachable.
func New(addr string) *Client {
	base := addr
	if !strings.Contains(base, "://") {
		base = "http://" + base
	}
	return &Client{
		base: strings.TrimRight(base, "/"),
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

// Addr returns the server base URL.
func (c *Client) Addr() string { return c.base }

func (c *Client) do(method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	return c.http.Do(req)
}

func keyPath(key string) string {
	return "/v1/kv/" + url.PathEscape(key)
}

// apiError reads an error response's JSON body into an *APIError.
func apiError(res *http.Response) error {
	var body struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil || body.Error == "" {
		body.Error = http.StatusText(res.StatusCode)
	}
	return &APIError{Status: res.StatusCode, Message: body.Error}
}

// Ping checks liveness against the server.
func (c *Client) Ping() (string, error) {
	res, err := c.do(http.MethodGet, "/v1/ping", nil)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", apiError(res)
	}
	b, err := io.ReadAll(res.Body)
	return string(b), err
}

// Get returns the value for key. found is false when the key does
// not exist, not an error.
func (c *Client) Get(key string) (value string, found bool, err error) {
	res, err := c.do(http.MethodGet, keyPath(key), nil)
	if err != nil {
		return "", false, err
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK:
		b, err := io.ReadAll(res.Body)
		if err != nil {
			return "", false, err
		}
		return string(b), true, nil
	case http.StatusNotFound:
		return "", false, nil
	default:
		return "", false, apiError(res)
	}
}

// Set stores value under key. A ttl of 0 means the key never expires.
func (c *Client) Set(key, value string, ttl time.Duration) error {
	path := keyPath(key)
	if ttl > 0 {
		secs := (ttl + time.Second - 1) / time.Second
		path += "?ttl=" + strconv.FormatInt(int64(secs), 10)
	}
	res, err := c.do(http.MethodPut, path, strings.NewReader(value))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		return apiError(res)
	}
	return nil
}

// TTL returns the time left before key expires, or 0 if it never expires.
func (c *Client) TTL(key string) (remaining time.Duration, found bool, err error) {
	res, err := c.do(http.MethodGet, "/v1/ttl/"+url.PathEscape(key), nil)
	if err != nil {
		return 0, false, err
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK:
		var body struct {
			TTL int64 `json:"ttl"`
		}
		if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
			return 0, false, fmt.Errorf("client: decoding ttl: %w", err)
		}
		if body.TTL < 0 {
			return 0, true, nil
		}
		return time.Duration(body.TTL) * time.Second, true, nil
	case http.StatusNotFound:
		return 0, false, nil
	default:
		return 0, false, apiError(res)
	}
}

// Delete removes key and reports whether it existed.
func (c *Client) Delete(key string) (bool, error) {
	res, err := c.do(http.MethodDelete, keyPath(key), nil)
	if err != nil {
		return false, err
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusNoContent:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, apiError(res)
	}
}

// Keys returns every key currently stored, sorted.
func (c *Client) Keys() ([]string, error) {
	res, err := c.do(http.MethodGet, "/v1/keys", nil)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, apiError(res)
	}
	var body struct {
		Keys []string `json:"keys"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("client: decoding keys: %w", err)
	}
	return body.Keys, nil
}

// Execute parses a plain command line the way the CLI's input box
// does (whitespace-separated fields), runs it, and returns the reply as
// a terminal would print it. Bad command lines return a *CommandError.
func (c *Client) Execute(line string) (string, error) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", &CommandError{"ERR empty command"}
	}
	name, args := strings.ToUpper(fields[0]), fields[1:]
	nargs := map[string]int{"PING": 0, "GET": 1, "SET": 2, "DEL": 1, "KEYS": 1, "TTL": 1}
	want, ok := nargs[name]
	if !ok {
		return "", &CommandError{fmt.Sprintf("ERR unknown command '%s'", strings.ToLower(fields[0]))}
	}
	if name == "SET" && len(args) == 4 { // SET key value EX seconds
		want = 4
	}
	if len(args) != want {
		return "", &CommandError{fmt.Sprintf("ERR wrong number of arguments for '%s' command", strings.ToLower(name))}
	}

	switch name {
	case "PING":
		return c.Ping()
	case "GET":
		v, found, err := c.Get(args[0])
		if err != nil || !found {
			return "(nil)", err
		}
		return v, nil
	case "SET":
		var ttl time.Duration
		if len(args) == 4 {
			if !strings.EqualFold(args[2], "EX") {
				return "", &CommandError{"ERR syntax error"}
			}
			secs, err := strconv.ParseInt(args[3], 10, 64)
			if err != nil || secs <= 0 || secs > math.MaxInt64/int64(time.Second) {
				return "", &CommandError{"ERR invalid expire time in 'set' command"}
			}
			ttl = time.Duration(secs) * time.Second
		}
		if err := c.Set(args[0], args[1], ttl); err != nil {
			return "", err
		}
		return "OK", nil
	case "TTL": // -2 missing, -1 no expiry, else seconds left
		remaining, found, err := c.TTL(args[0])
		switch {
		case err != nil:
			return "", err
		case !found:
			return "-2", nil
		case remaining == 0:
			return "-1", nil
		}
		return strconv.FormatInt(int64(remaining/time.Second), 10), nil
	case "DEL":
		existed, err := c.Delete(args[0])
		if err != nil {
			return "", err
		}
		if existed {
			return "1", nil
		}
		return "0", nil
	default: // KEYS
		if args[0] != "*" {
			return "", &CommandError{"ERR KEYS only supports the '*' pattern for now"}
		}
		keys, err := c.Keys()
		if err != nil {
			return "", err
		}
		if len(keys) == 0 {
			return "(empty array)", nil
		}
		return strings.Join(keys, "\n"), nil
	}
}

// IsTransportError reports whether err came from failing to reach the
// server, as opposed to the server or the command line rejecting a
// request.
func IsTransportError(err error) bool {
	var urlErr *url.Error
	return errors.As(err, &urlErr)
}
