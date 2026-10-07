# Gotun Makefile

APP_NAME = gotun
BUILD_DIR = bin
CMD_PATH = ./cmd/gotun

.PHONY: all clean build build-mac build-linux build-win

all: clean build build-mac build-linux build-win

clean:
	@echo "Cleaning up..."
	@rm -rf $(BUILD_DIR)

build:
	@echo "Building for current platform..."
	@go build -o $(BUILD_DIR)/$(APP_NAME) $(CMD_PATH)

build-mac:
	@echo "Building for macOS (amd64 & arm64)..."
	@GOOS=darwin GOARCH=amd64 go build -o $(BUILD_DIR)/$(APP_NAME)_darwin_amd64 $(CMD_PATH)
	@GOOS=darwin GOARCH=arm64 go build -o $(BUILD_DIR)/$(APP_NAME)_darwin_arm64 $(CMD_PATH)

build-linux:
	@echo "Building for Linux (amd64 & arm64)..."
	@GOOS=linux GOARCH=amd64 go build -o $(BUILD_DIR)/$(APP_NAME)_linux_amd64 $(CMD_PATH)
	@GOOS=linux GOARCH=arm64 go build -o $(BUILD_DIR)/$(APP_NAME)_linux_arm64 $(CMD_PATH)

build-win:
	@echo "Building for Windows (amd64)..."
	@GOOS=windows GOARCH=amd64 go build -o $(BUILD_DIR)/$(APP_NAME)_windows_amd64.exe $(CMD_PATH)
