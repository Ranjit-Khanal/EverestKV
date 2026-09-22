APP      := everestkv
CLI      := everestkv-cli
WEB      := everestkv-web
BIN_DIR  := bin
BIN      := $(BIN_DIR)/$(APP)
CLI_BIN  := $(BIN_DIR)/$(CLI)
WEB_BIN  := $(BIN_DIR)/$(WEB)

.PHONY: all build server cli web run run-cli run-web clean

all: build

build: server cli web

server:
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN) ./cmd/$(APP)

cli:
	@mkdir -p $(BIN_DIR)
	go build -o $(CLI_BIN) ./cmd/$(APP)/cli

web:
	@mkdir -p $(BIN_DIR)
	go build -o $(WEB_BIN) ./cmd/$(APP)/web

run: server
	./$(BIN)

run-cli: cli
	./$(CLI_BIN)

run-web: web
	./$(WEB_BIN)

clean:
	rm -rf $(BIN_DIR)
