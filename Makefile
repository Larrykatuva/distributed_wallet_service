BINARY_NAME=wallet
CMD_DIR=./cmd
K8S_DIR=k8
PROTO_DIR=proto
GOBIN?=$(shell go env GOPATH)/bin
export PATH:=$(GOBIN):$(PATH)

.PHONY: all build run test vet lint tidy clean docker-build docker-run \
        compose-up compose-down compose-logs compose-ps \
        k8s-render k8s-apply k8s-delete proto migrate-up migrate-down

all: build

build:
	@echo "Building $(BINARY_NAME)..."
	@go build -o $(BINARY_NAME) $(CMD_DIR)

run:
	@go run $(CMD_DIR)

test:
	@go test ./...

vet:
	@go vet ./...

# Uses golangci-lint if installed, otherwise falls back to go vet.
lint:
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run ./... || go vet ./...

tidy:
	@go mod tidy

clean:
	@rm -f $(BINARY_NAME)

migrate-up:
	@go run $(CMD_DIR) migrate up

migrate-down:
	@go run $(CMD_DIR) migrate down

# Regenerate protobuf/gRPC code. Needs protoc, protoc-gen-go and protoc-gen-go-grpc.
proto:
	@protoc -I $(PROTO_DIR) \
		--go_out=paths=source_relative:$(PROTO_DIR) \
		--go-grpc_out=paths=source_relative:$(PROTO_DIR) \
		$(PROTO_DIR)/actors/actors.proto \
		$(PROTO_DIR)/profile/profile.proto \
		$(PROTO_DIR)/wallet/wallet.proto \
		$(PROTO_DIR)/transaction/transaction.proto

docker-build:
	@docker build -t $(BINARY_NAME) .

docker-run:
	@docker run --rm -p 3003:3003 -p 3004:3004 --env-file .env $(BINARY_NAME)

# Docker Compose stack: postgres + redis + wallet (+ pgbouncer with --profile pooler)
compose-up:
	@docker compose up -d --build

compose-down:
	@docker compose down

compose-logs:
	@docker compose logs -f wallet

compose-ps:
	@docker compose ps

k8s-render:
	@kubectl kustomize $(K8S_DIR)

k8s-apply:
	@kubectl apply -k $(K8S_DIR)

k8s-delete:
	@kubectl delete -k $(K8S_DIR)
