# Go binary name
BINARY_NAME=wallet

# Go-related directories
CMD_DIR=cmd
GO_FILES=$(shell find . -name '*.go')

# Default target: `make` will run this by default
.PHONY: all
all: build

# Build the application binary
.PHONY: build
build:
	@echo "Building the jp_gateway application..."
	@go build -o $(BINARY_NAME) cmd/api/main.go

# Run the application (without building again)
.PHONY: run
run:
	@echo "Running the wallet application..."
	@go run $(CMD_DIR)/main.go

# Run tests (using Go's testing framework)
.PHONY: test
test:
	@echo "Running wallet tests..."
	@go test ./...

# Clean the Go build artifacts
.PHONY: clean
clean:
	@echo "Cleaning wallet up..."
	@rm -f $(BINARY_NAME)

# Install dependencies (equivalent to `go mod tidy`)
.PHONY: deps
deps:
	@echo "Fetching wallet dependencies..."
	@go mod tidy

# Run the application with `make start`
.PHONY: start
start: build run

# Run application in Docker
.PHONY: docker-build
docker-build:
	@echo "Building wallet Docker image..."
	@docker build -t $(BINARY_NAME) .

# Run the application in Docker (after building the image)
.PHONY: docker-run
docker-run:
	@echo "Running wallet Docker container..."
	@docker run -p 3000:3000 $(BINARY_NAME)

# Generate swagger docs
swag:
	@echo "Generating wallet swagger documentation...."
	swag fmt
	swag init -g $(CMD_DIR)/main.go -o ./docs --parseInternal true