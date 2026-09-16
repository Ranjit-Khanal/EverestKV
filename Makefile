APP      := everestkv
CLI      := everestkv-cli
BIN_DIR  := bin
BIN      := $(BIN_DIR)/$(APP)
CLI_BIN  := $(BIN_DIR)/$(CLI)

.PHONY: all build server cli run run-cli clean

all: build

build: server cli

server:
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN) ./cmd/$(APP)

cli:
	@mkdir -p $(BIN_DIR)
	go build -o $(CLI_BIN) ./cmd/$(APP)/cli

run: server
	./$(BIN)

run-cli: cli
	./$(CLI_BIN)

clean:
	rm -rf $(BIN_DIR)
