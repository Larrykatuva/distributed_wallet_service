# syntax=docker/dockerfile:1

# --- Build stage -------------------------------------------------------------
FROM golang:1.25-alpine AS builder

WORKDIR /src

# Cache dependency downloads separately from source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/wallet ./cmd

# --- Runtime stage -------------------------------------------------------------
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S wallet \
    && adduser -S wallet -G wallet

WORKDIR /app
COPY --from=builder /out/wallet ./wallet

# The app's logger creates ./logs relative to its working directory at startup
# (dpk/logger/logger.go) — the non-root user below needs write access to do that.
RUN mkdir -p /app/logs && chown -R wallet:wallet /app

USER wallet

# HTTP, gRPC, and Proto.Actor remoting ports (see config/config.go).
EXPOSE 3003 3004 8090

ENTRYPOINT ["./wallet"]
