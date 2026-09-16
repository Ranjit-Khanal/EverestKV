package command

import (
	"github.com/Ranjit-Khanal/everestkv/internal/store"
	"github.com/Ranjit-Khanal/everestkv/pkg/resp"
)

func ping(_ *store.Store, _ []string, w *resp.Writer) error {
	return w.WriteSimpleString("PONG")
}

func echo(_ *store.Store, args []string, w *resp.Writer) error {
	if len(args) != 1 {
		return w.WriteError("ERR wrong number of arguments for 'echo' command")
	}
	return w.WriteBulkString(args[0])
}

func exit(_ *store.Store, _ []string, w *resp.Writer) error {
	if err := w.WriteSimpleString("OK"); err != nil {
		return err
	}
	return ErrQuit
}
