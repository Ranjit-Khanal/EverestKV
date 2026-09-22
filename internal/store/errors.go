package store

import "errors"

// ErrClosed is returned by DB operations called after Close.
var ErrClosed = errors.New("store: closed")
