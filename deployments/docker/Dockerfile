# Multi-stage build for LLM Token Gateway
# Produces a minimal scratch-based image (~15MB)

FROM golang:1.23-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s -X main.version=$(git describe --tags --always)" \
    -o /build/llm-token-gateway \
    ./cmd/gateway

# --- Runtime ---
FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /build/llm-token-gateway /llm-token-gateway
COPY configs/gateway.yaml /etc/gateway/gateway.yaml

EXPOSE 8443

ENTRYPOINT ["/llm-token-gateway"]
CMD ["--config", "/etc/gateway/gateway.yaml"]
