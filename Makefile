# Gotun Makefile

APP_NAME = gotun
BUILD_DIR = bin
CMD_PATH = ./cmd/gotun

.PHONY: all clean build build-mac build-linux build-win

all: clean build-mac build-linux build-win

clean:
	@echo "Cleaning up..."
	@rm -rf $(BUILD_DIR)

build:
	@mkdir -p $(BUILD_DIR)
	@echo "Building for current platform..."
	@go build -o $(BUILD_DIR)/$(APP_NAME) $(CMD_PATH)
	@echo "编译完成！请直接运行 ./bin/gotun"

build-mac:
	@mkdir -p $(BUILD_DIR)
	@echo "Building natively for macOS (CGO enabled for systray)..."
	@CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 go build -o $(BUILD_DIR)/$(APP_NAME)_darwin_amd64 $(CMD_PATH)
	@CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -o $(BUILD_DIR)/$(APP_NAME)_darwin_arm64 $(CMD_PATH)

build-linux:
	@mkdir -p $(BUILD_DIR)
	@echo "Building for Linux (amd64 & arm64, Headless)..."
	@CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(BUILD_DIR)/$(APP_NAME)_linux_amd64 $(CMD_PATH)
	@CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o $(BUILD_DIR)/$(APP_NAME)_linux_arm64 $(CMD_PATH)

build-win:
	@mkdir -p $(BUILD_DIR)
	@echo "Building for Windows (amd64)..."
	@GOOS=windows GOARCH=amd64 go build -o $(BUILD_DIR)/$(APP_NAME)_windows_amd64.exe $(CMD_PATH)
