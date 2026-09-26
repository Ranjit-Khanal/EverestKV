// Command everestkv runs the EverestKV server: a RESP2-speaking TCP
// server listening on :6379, so redis-cli and other Redis clients can talk
// to it directly.
package main

import (
	"log"

	"github.com/Ranjit-Khanal/everestkv/internal/server"
)

func main() {
	srv := server.New(server.DefaultConfig())
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
