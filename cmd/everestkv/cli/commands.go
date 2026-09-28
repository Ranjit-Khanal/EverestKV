package main

// Command is a CLI command name.
type Command string

const (
	CommandPing Command = "PING"
	CommandGet  Command = "GET"
	CommandSet  Command = "SET"
	CommandDel  Command = "DEL"
	CommandKeys Command = "KEYS"
	CommandQuit Command = "QUIT" // local only — same as EXIT
	CommandExit Command = "EXIT" // local only — closes CLI without talking to the server
)

func (c Command) String() string {
	return string(c)
}
