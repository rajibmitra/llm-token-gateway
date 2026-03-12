#!/usr/bin/env bash
# Quick setup for local development
set -euo pipefail

echo "=== LLM Token Gateway — Local Setup ==="
echo ""

# Check Go installation
if ! command -v go &>/dev/null; then
    echo "ERROR: Go is not installed. Install Go 1.23+ from https://go.dev"
    exit 1
fi
echo "✓ Go $(go version | awk '{print $3}')"

# Check API keys
missing_keys=()
[[ -z "${ANTHROPIC_API_KEY:-}" ]] && missing_keys+=("ANTHROPIC_API_KEY")
[[ -z "${OPENAI_API_KEY:-}" ]]    && missing_keys+=("OPENAI_API_KEY")

if [[ ${#missing_keys[@]} -gt 0 ]]; then
    echo ""
    echo "WARNING: The following API keys are not set:"
    for key in "${missing_keys[@]}"; do
        echo "  - $key"
    done
    echo ""
    echo "Set them before running the gateway:"
    echo "  export ANTHROPIC_API_KEY=sk-ant-..."
    echo "  export OPENAI_API_KEY=sk-..."
    echo ""
fi

# Install dependencies
echo "Installing Go dependencies..."
go mod tidy

# Build
echo "Building gateway..."
go build -o bin/llm-token-gateway ./cmd/gateway
echo "✓ Built: bin/llm-token-gateway"

echo ""
echo "=== Setup complete ==="
echo ""
echo "To run the gateway:"
echo "  make run"
echo ""
echo "Then configure your agents:"
echo ""
echo "  Claude Code:"
echo "    export ANTHROPIC_BASE_URL=http://localhost:8443"
echo ""
echo "  Cursor:"
echo "    Settings → Models → API Base URL → http://localhost:8443"
echo ""
echo "  Aider:"
echo "    aider --openai-api-base http://localhost:8443"
echo ""
echo "Monitor savings:"
echo "  make stats"
