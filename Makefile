APP      := everestkv
CLI      := everestkv-cli
BIN_DIR  := bin
BIN      := $(BIN_DIR)/$(APP)
CLI_BIN  := $(BIN_DIR)/$(CLI)

.PHONY: all build cli run clean

all: build cli

build:
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN) ./cmd/$(APP)

cli:
	@mkdir -p $(BIN_DIR)
	go build -o $(CLI_BIN) ./cmd/$(APP)/cli

run: build
	./$(BIN)

clean:
	rm -rf $(BIN_DIR)
