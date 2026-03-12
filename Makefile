.PHONY: build run test lint docker-build docker-run clean

BINARY_NAME=llm-token-gateway
VERSION?=$(shell git describe --tags --always 2>/dev/null || echo "dev")
LDFLAGS=-ldflags "-w -s -X main.version=$(VERSION)"

# Build
build:
	go build $(LDFLAGS) -o bin/$(BINARY_NAME) ./cmd/gateway

# Run locally
run: build
	./bin/$(BINARY_NAME) --config configs/gateway.yaml

# Run with hot-reload (requires air: go install github.com/air-verse/air@latest)
dev:
	air -c .air.toml

# Test
test:
	go test -v -race -coverprofile=coverage.out ./...

test-cover: test
	go tool cover -html=coverage.out -o coverage.html

# Benchmark the optimizer
bench:
	go test -bench=. -benchmem ./internal/optimizer/... ./internal/classifier/...

# Lint
lint:
	golangci-lint run ./...

# Docker
docker-build:
	docker build -t $(BINARY_NAME):$(VERSION) -f deployments/docker/Dockerfile .

docker-run: docker-build
	docker run -p 8443:8443 \
		-e ANTHROPIC_API_KEY=$(ANTHROPIC_API_KEY) \
		-e OPENAI_API_KEY=$(OPENAI_API_KEY) \
		$(BINARY_NAME):$(VERSION)

# K8s deployment
k8s-deploy:
	kubectl apply -f deployments/k8s/daemonset.yaml

k8s-delete:
	kubectl delete -f deployments/k8s/daemonset.yaml

# Quick test: send a request through the gateway
smoke-test:
	@echo "Testing Anthropic passthrough..."
	curl -s -X POST http://localhost:8443/v1/messages \
		-H "Content-Type: application/json" \
		-H "X-LLM-Gateway-Agent: smoke-test" \
		-d '{"model":"claude-sonnet-4-6","max_tokens":50,"messages":[{"role":"user","content":"Say hello"}]}' \
		| python3 -m json.tool

# Check gateway stats
stats:
	@curl -s http://localhost:8443/gateway/stats | python3 -m json.tool

health:
	@curl -s http://localhost:8443/gateway/health | python3 -m json.tool

clean:
	rm -rf bin/ coverage.out coverage.html
