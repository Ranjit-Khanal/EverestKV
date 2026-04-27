package main

import (
	"fmt"
	"net"
)

func main() {
	// Start TCP server on port 6379 (same as Redis)
	listener, err := net.Listen("tcp", ":6379")
	if err != nil {
		panic(err)
	}
	defer listener.Close()

	fmt.Println("Server running on port 6379...")

	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Println("Error accepting:", err)
			continue
		}

		// Handle each client in a goroutine
		go handleConnection(conn)
	}
}

func handleConnection(conn net.Conn) {
	defer conn.Close()

	fmt.Println("New client connected:", conn.RemoteAddr())

	buffer := make([]byte, 1024)

	for {
		n, err := conn.Read(buffer)
		if err != nil {
			fmt.Println("Client disconnected")
			return
		}

		data := string(buffer[:n])
		fmt.Println("Received:", data)

		// Echo back (temporary)
		conn.Write([]byte("OK\n"))
	}
}