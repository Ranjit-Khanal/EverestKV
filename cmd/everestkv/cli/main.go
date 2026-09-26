// Command everestkv-cli is an interactive prompt for an EverestKV server.
// Each line is split on whitespace and sent as one RESP2 command; the
// reply is printed the same way the web dashboard console shows it.
// Typing EXIT leaves the prompt without sending anything to the server.
//
// Usage:
//
//	everestkv-cli [-addr localhost:6379]
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
	addr := flag.String("addr", "localhost:6379", "EverestKV server address")
	flag.Parse()

	c, err := client.Dial(*addr)
	if err != nil {
		fmt.Printf("Error connecting to server: %v\n", err)
		os.Exit(1)
	}
	defer c.Close()

	fmt.Printf("Connected to EverestKv at %s\n", *addr)
	fmt.Printf("Type your commands (e.g., %s, %s hello, %s) or %s to leave\n",
		CommandPing, CommandEcho, CommandQuit, CommandExit)

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
		if Command(strings.ToUpper(line)) == CommandExit {
			fmt.Println("Bye!")
			return
		}

		v, err := c.Execute(line)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}
		fmt.Println(client.FormatReply(v))
	}
}
