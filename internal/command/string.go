package command

import (
	"github.com/Ranjit-Khanal/everestkv/internal/store"
	"github.com/Ranjit-Khanal/everestkv/pkg/resp"
)

func get(s *store.Store, args []string, w *resp.Writer) error {
	if len(args) != 1 {
		return w.WriteError("ERR wrong number of arguments for 'get' command")
	}
	val, ok := s.Get(args[0])
	if !ok {
		return w.WriteNullBulk()
	}
	return w.WriteBulkString(val)
}

func set(s *store.Store, args []string, w *resp.Writer) error {
	if len(args) != 2 {
		return w.WriteError("ERR wrong number of arguments for 'set' command")
	}
	s.Set(args[0], args[1])
	return w.WriteSimpleString("OK")
}
