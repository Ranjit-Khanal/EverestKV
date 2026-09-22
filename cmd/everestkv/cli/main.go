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
