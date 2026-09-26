// Package command implements EverestKV's commands. Each command is a
// Handler registered by uppercase name in Registry; Dispatch looks the
// name up case-insensitively and runs it against the store, writing the
// RESP2 reply. Adding a command means writing a Handler and adding one
// line to Registry — nothing in internal/server needs to change.
package command

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Ranjit-Khanal/everestkv/internal/store"
	"github.com/Ranjit-Khanal/everestkv/pkg/resp"
)

// ErrQuit signals the connection should close after a successful reply.
var ErrQuit = errors.New("quit")

// Handler executes a command and writes the RESP reply.
type Handler func(s *store.Store, args []string, w *resp.Writer) error

// Registry maps uppercase command names to handlers.
var Registry = map[string]Handler{
	"PING": ping,
	"ECHO": echo,
	"GET":  get,
	"SET":  set,
	"KEYS": keys,
	"EXIT": exit,
}

// Dispatch looks up cmd and runs it. Unknown commands return a RESP error.
func Dispatch(s *store.Store, cmd string, args []string, w *resp.Writer) error {
	h, ok := Registry[strings.ToUpper(cmd)]
	if !ok {
		return w.WriteError(fmt.Sprintf("ERR unknown command '%s'", strings.ToLower(cmd)))
	}
	return h(s, args, w)
}
