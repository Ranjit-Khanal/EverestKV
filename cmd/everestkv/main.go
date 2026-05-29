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
