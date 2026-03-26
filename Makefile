BINARY   := molt
SRC_DIR  := ./src
BUILD_DIR := ./build
PREFIX   := $(HOME)/.local

.PHONY: all build build-drivers build-all clean test install install-drivers install-all

all: build

build:
	@mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/$(BINARY) $(SRC_DIR)
	@echo "Built: $(BUILD_DIR)/$(BINARY)"

install: build
	@mkdir -p $(PREFIX)/bin
	cp $(BUILD_DIR)/$(BINARY) $(PREFIX)/bin/$(BINARY)
	@echo "Installed: $(PREFIX)/bin/$(BINARY)"

build-drivers:
	@mkdir -p $(BUILD_DIR)
	@for d in drivers/*/; do \
		name=$$(basename $$d); \
		echo "Building molt-driver-$$name..."; \
		(cd $$d && go build -o ../../$(BUILD_DIR)/molt-driver-$$name .) || exit 1; \
		echo "Built: $(BUILD_DIR)/molt-driver-$$name"; \
	done

install-drivers: build-drivers
	@mkdir -p $(PREFIX)/bin
	@for bin in $(BUILD_DIR)/molt-driver-*; do \
		cp $$bin $(PREFIX)/bin/; \
		echo "Installed: $(PREFIX)/bin/$$(basename $$bin)"; \
	done

install-all: install install-drivers

test:
	go test ./...
	@for d in drivers/*/; do \
		echo "Testing $$(basename $$d) driver..."; \
		(cd $$d && go test ./...) || exit 1; \
	done

clean:
	rm -rf $(BUILD_DIR)

# Cross-compile for common targets
build-all:
	GOOS=darwin  GOARCH=arm64  go build -o $(BUILD_DIR)/$(BINARY)-darwin-arm64  $(SRC_DIR)
	GOOS=darwin  GOARCH=amd64  go build -o $(BUILD_DIR)/$(BINARY)-darwin-amd64  $(SRC_DIR)
	GOOS=linux   GOARCH=amd64  go build -o $(BUILD_DIR)/$(BINARY)-linux-amd64   $(SRC_DIR)
	GOOS=linux   GOARCH=arm64  go build -o $(BUILD_DIR)/$(BINARY)-linux-arm64   $(SRC_DIR)
	@echo "Cross-compiled binaries in $(BUILD_DIR)/"
