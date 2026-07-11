package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
)

func main() {
	// Connect to the server
	conn, err := net.Dial("tcp", "localhost:6379")
	if err != nil {
		fmt.Printf("Error connecting to server: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	fmt.Println("Connected to EverestKv at localhost:6379")
	fmt.Println("Type your commands (e.g., PING, ECHO hello, QUIT)")

	// Read from stdin and send to server
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
		if strings.ToUpper(line) == "QUIT" {
			fmt.Println("Bye!")
			return
		}

		// Send the command
		if err := sendCommand(conn, line); err != nil {
			fmt.Printf("Error sending command: %v\n", err)
			continue
		}

		// Read and print the response
		if err := readResponse(conn); err != nil {
			fmt.Printf("Error reading response: %v\n", err)
		}
	}
}

// sendCommand converts a plain text command to RESP and sends it
func sendCommand(conn net.Conn, cmd string) error {
	parts := strings.Fields(cmd)
	resp := formatRESP(parts)
	_, err := conn.Write([]byte(resp))
	return err
}

// formatRESP converts a slice of strings to RESP array format
func formatRESP(args []string) string {
	resp := fmt.Sprintf("*%d\r\n", len(args))
	for _, arg := range args {
		resp += fmt.Sprintf("$%d\r\n%s\r\n", len(arg), arg)
	}
	return resp
}

// readResponse reads and prints the server's response
func readResponse(conn net.Conn) error {
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Handle different RESP types
		switch line[0] {
		case '+': // Simple string
			fmt.Println(line[1:])
			return nil
		case '-': // Error
			fmt.Printf("ERR %s\n", line[1:])
			return nil
		case ':': // Integer
			fmt.Println(line[1:])
			return nil
		case '$': // Bulk string
			return readBulkString(reader, line)
		case '*': // Array
			return readArray(reader, line)
		default:
			fmt.Println(line)
			return nil
		}
	}
}

// readBulkString reads a RESP bulk string response
func readBulkString(reader *bufio.Reader, line string) error {
	var length int
	fmt.Sscanf(line, "$%d", &length)
	if length < 0 {
		fmt.Println("(nil)")
		return nil
	}
	if length == 0 {
		fmt.Println("")
		return nil
	}
	// Read the bulk data
	data := make([]byte, length)
	_, err := io.ReadFull(reader, data)
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	// Read the trailing \r\n
	reader.Discard(2)
	return nil
}

// readArray reads a RESP array response (simple version)
func readArray(reader *bufio.Reader, line string) error {
	var count int
	fmt.Sscanf(line, "*%d", &count)
	if count == 0 {
		fmt.Println("(empty array)")
		return nil
	}
	if count < 0 {
		fmt.Println("(nil)")
		return nil
	}
	fmt.Println("Array response:")
	for i := 0; i < count; i++ {
		subLine, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		subLine = strings.TrimSpace(subLine)
		if len(subLine) > 0 && subLine[0] == '$' {
			readBulkString(reader, subLine)
		} else {
			fmt.Println(subLine)
		}
	}
	return nil
}
