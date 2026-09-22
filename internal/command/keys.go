package command

import (
	"github.com/Ranjit-Khanal/everestkv/internal/store"
	"github.com/Ranjit-Khanal/everestkv/pkg/resp"
)

// keys implements a minimal KEYS: only the "*" pattern (every key) is
// supported today — no globbing, matching the store's current scope.
func keys(s *store.Store, args []string, w *resp.Writer) error {
	if len(args) != 1 {
		return w.WriteError("ERR wrong number of arguments for 'keys' command")
	}
	if args[0] != "*" {
		return w.WriteError("ERR KEYS only supports the '*' pattern for now")
	}

	all := s.Keys()
	items := make([]resp.Value, len(all))
	for i, k := range all {
		items[i] = resp.Value{Type: resp.TypeBulkString, Bulk: []byte(k)}
	}
	return w.Write(resp.Value{Type: resp.TypeArray, Array: items})
}
