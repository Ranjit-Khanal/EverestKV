package main

// Command is a CLI / wire command name.
type Command string

const (
	CommandPing Command = "PING"
	CommandEcho Command = "ECHO"
	CommandGet  Command = "GET"
	CommandSet  Command = "SET"
	CommandQuit Command = "QUIT"
	CommandExit Command = "EXIT" // local only — closes CLI without talking to the server
)

func (c Command) String() string {
	return string(c)
}
