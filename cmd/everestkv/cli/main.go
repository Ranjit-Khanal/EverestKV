// Command everestkv-cli is an interactive prompt for an EverestKV server.
// Each line is split on whitespace and turned into one HTTP request; the
// reply is printed the same way the web dashboard console shows it.
// Typing EXIT or QUIT leaves the prompt without contacting the server.
//
// Usage:
//
//	everestkv-cli [-addr localhost:8379]
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Ranjit-Khanal/everestkv/internal/client"
)

func main() {
	addr := flag.String("addr", "localhost:8379", "EverestKV server address")
	flag.Parse()

	c := client.New(*addr)
	if _, err := c.Ping(); err != nil {
		fmt.Printf("Error connecting to server: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Connected to EverestKv at %s\n", *addr)
	fmt.Printf("Type your commands (e.g., %s, %s k v, %s k, %s k, %s *) or %s to leave\n",
		CommandPing, CommandSet, CommandGet, CommandDel, CommandKeys, CommandExit)

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if cmd := Command(strings.ToUpper(line)); cmd == CommandExit || cmd == CommandQuit {
			fmt.Println("Bye!")
			return
		}

		reply, err := c.Execute(line)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}
		fmt.Println(reply)
	}
}
