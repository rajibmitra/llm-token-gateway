# LLM Token Gateway

A transparent reverse proxy that reduces LLM API costs by optimizing token usage across all your AI coding agents (Claude Code, Cursor, Aider, Continue, Copilot, and more).

![Architecture](images/01.jpg)

## How it works

The gateway sits between your agents and the provider APIs (Anthropic, OpenAI, Google Gemini). It intercepts every request, runs an optimization pipeline on the message payload, then forwards to the real API — completely transparent to the agent.

```
Agent (Claude Code / Cursor / Aider)
        │  POST /v1/messages
        ▼
┌───────────────────────┐
│   LLM Token Gateway   │
│  ┌─────────────────┐  │
│  │  Prompt Cache   │  │  ← skip upstream entirely on hit
│  ├─────────────────┤  │
│  │   Optimizer     │  │  ← TOON, compact JSON, dedup, whitespace
│  ├─────────────────┤  │
│  │  Metrics/Stats  │  │  ← per-agent token savings + cost
│  └─────────────────┘  │
└───────────────────────┘
        │
        ▼
  Anthropic / OpenAI / Gemini API
```

### Optimization strategies

| Strategy | Typical saving | How |
|---|---|---|
| **TOON encoding** | ~40% on structured data | Converts uniform JSON arrays to compact tabular format |
| **JSON compaction** | ~20% on pretty-printed JSON | Minifies whitespace in embedded JSON blobs |
| **Prompt caching** | 100% on repeated context | SHA-256 dedup of identical requests |
| **Tool result dedup** | Varies | Replaces repeated tool results in long conversations |
| **Whitespace stripping** | ~5–10% on plain text | Collapses excess blank lines and trailing spaces |

---

## Quick start

### Prerequisites

- Go 1.23+
- An API key for at least one provider

### Run locally

```bash
# Clone
git clone https://github.com/rajibmitra/llm-token-gateway.git
cd llm-token-gateway

# Set API keys (never hardcode these)
export ANTHROPIC_API_KEY="sk-ant-..."
export OPENAI_API_KEY="sk-..."
export GEMINI_API_KEY="..."

# Build and run
make run
```

Or use the setup script for first-time setup:

```bash
chmod +x scripts/setup.sh
./scripts/setup.sh
```

### Point your agent at the gateway

**Claude Code:**
```bash
export ANTHROPIC_BASE_URL=http://localhost:8443
```

**Cursor:** Settings → Models → API Base URL → `http://localhost:8443`

**Aider:**
```bash
aider --openai-api-base http://localhost:8443
```

**Any OpenAI-compatible agent:**
```bash
export OPENAI_BASE_URL=http://localhost:8443
```

---

## Configuration

The main config file is `configs/gateway.yaml`. A minimal dev config is at `configs/gateway-dev.yaml`.

### Provider configuration

```yaml
providers:
  anthropic:
    base_url: "https://api.anthropic.com"
    api_key_env: "ANTHROPIC_API_KEY"   # env var name — never put the key here
    rate_limit_rpm: 1000
    max_retries: 3

  openai:
    base_url: "https://api.openai.com"
    api_key_env: "OPENAI_API_KEY"
    rate_limit_rpm: 500
    max_retries: 3
```

### Per-agent optimization profiles

Each detected agent can have its own tuning:

```yaml
agents:
  claude-code:
    toon_threshold: 0.6      # Claude Code sends lots of tool results
    compact_json: true
    cache_ttl: "300s"
    spend_limit_usd: 100.0   # Monthly cap (informational)

  cursor:
    toon_threshold: 0.8      # Cursor sends more code, higher threshold
    cache_ttl: "120s"
    spend_limit_usd: 50.0
```

Agents are identified automatically from the `User-Agent` header or the `X-LLM-Gateway-Agent` header (for self-identification).

### Optimizer settings

```yaml
optimizer:
  toon_enabled: true
  compact_json_enabled: true
  strip_whitespace: true
  deduplicate_tool_results: true
  min_savings_percent: 5.0      # Skip optimization if saving < 5%
  max_payload_size_kb: 5120     # Skip optimization on very large payloads
```

### Cache backends

```yaml
cache:
  enabled: true
  backend: "memory"    # "memory" for local dev, "redis" for production
  redis_url: ""        # e.g. "localhost:6379"
  max_size_mb: 256
  default_ttl: "300s"
```

---

## Monitoring

```bash
# Gateway stats: tokens saved, cost reduction per agent
make stats

# Health check
make health

# Prometheus metrics endpoint
curl http://localhost:8443/gateway/metrics

# Flush prompt cache
curl -X POST http://localhost:8443/gateway/cache/flush
```

Example stats response:

```json
{
  "agents": {
    "claude-code": {
      "requests": 142,
      "tokens_original": 890000,
      "tokens_optimized": 534000,
      "savings_percent": 40.0,
      "cost_saved_usd": 1.07
    }
  },
  "cache": {
    "hit_rate": "23.4%",
    "hits": 33,
    "misses": 109
  }
}
```

---

## API endpoints

| Endpoint | Method | Description |
|---|---|---|
| `/v1/messages` | POST | Anthropic Messages API proxy |
| `/v1/chat/completions` | POST | OpenAI Chat Completions proxy |
| `/v1beta/models/*` | POST | Google Gemini proxy |
| `/gateway/health` | GET | Health check |
| `/gateway/stats` | GET | Per-agent savings stats (JSON) |
| `/gateway/metrics` | GET | Prometheus metrics |
| `/gateway/cache/flush` | POST | Clear the prompt cache |

---

## Docker

```bash
# Build image
make docker-build

# Run with API keys
docker run -p 8443:8443 \
  -e ANTHROPIC_API_KEY=$ANTHROPIC_API_KEY \
  -e OPENAI_API_KEY=$OPENAI_API_KEY \
  llm-token-gateway:latest
```

---

## Kubernetes (DaemonSet)

Deploy as a DaemonSet so every node has a local gateway instance — agents connect to `localhost:8443` with no extra routing:

```bash
# Create secret with your API keys
kubectl create secret generic llm-api-keys \
  --from-literal=ANTHROPIC_API_KEY="$ANTHROPIC_API_KEY" \
  --from-literal=OPENAI_API_KEY="$OPENAI_API_KEY" \
  -n llm-gateway

# Deploy
make k8s-deploy
```

See `deployments/k8s/daemonset.yaml` for the full manifest (Namespace, ConfigMap, DaemonSet, Service, PodMonitor).

---

## Development

```bash
# Run tests
make test

# Run tests with coverage report
make test-cover

# Benchmark optimizer + classifier
make bench

# Lint
make lint

# Hot-reload (requires air)
go install github.com/air-verse/air@latest
make dev
```

---

## Project structure

```
├── cmd/gateway/          # main() — server setup, routing, graceful shutdown
├── internal/
│   ├── proxy/            # Core reverse proxy + optimization pipeline
│   ├── optimizer/        # TOON encoding, JSON compaction, whitespace stripping
│   ├── classifier/       # Content analysis (JSON detection, TOON eligibility)
│   ├── cache/            # Prompt cache (memory + Redis backends)
│   ├── metrics/          # Prometheus metrics + stats API
│   ├── middleware/        # HTTP middleware (request logger, agent ID, metrics)
│   └── config/           # YAML config loader
├── configs/
│   ├── gateway.yaml      # Full production config reference
│   └── gateway-dev.yaml  # Minimal local dev config
├── deployments/
│   ├── docker/           # Dockerfile (multi-stage, scratch-based ~15MB)
│   └── k8s/              # DaemonSet + RBAC + PodMonitor manifests
├── scripts/
│   └── setup.sh          # First-time local setup script
└── Makefile              # build, run, test, docker, k8s targets
```

---

## Security notes

- API keys are **never** stored in config files — they are read from environment variables at startup
- The `.gitignore` excludes `.env` files, TLS certificates, and compiled binaries
- The K8s deployment reads keys from a `Secret` via `envFrom`
- TLS is supported for production deployments (configure in `gateway.yaml`)
- The Docker image runs as a non-root user (`runAsUser: 65534`)

---

## License

MIT
