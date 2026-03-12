package middleware

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/rajibmitra/llm-token-gateway/internal/config"
	"github.com/rajibmitra/llm-token-gateway/internal/metrics"
	"go.uber.org/zap"
)

// contextKey is used for storing values in request context.
type contextKey string

const (
	AgentKey    contextKey = "agent"
	StartTimeKey contextKey = "start_time"
)

// Chain holds an ordered list of middleware functions.
type Chain struct {
	middlewares []func(http.Handler) http.Handler
}

// NewChain creates a middleware chain.
func NewChain(middlewares ...func(http.Handler) http.Handler) *Chain {
	return &Chain{middlewares: middlewares}
}

// Then applies the middleware chain to a final handler.
func (c *Chain) Then(h http.Handler) http.Handler {
	for i := len(c.middlewares) - 1; i >= 0; i-- {
		h = c.middlewares[i](h)
	}
	return h
}

// RequestLogger logs incoming requests with timing.
func RequestLogger(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ctx := context.WithValue(r.Context(), StartTimeKey, start)

			logger.Info("request",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.String("remote_addr", r.RemoteAddr),
				zap.String("user_agent", r.UserAgent()),
			)

			next.ServeHTTP(w, r.WithContext(ctx))

			logger.Info("response",
				zap.String("path", r.URL.Path),
				zap.Duration("duration", time.Since(start)),
			)
		})
	}
}

// AgentIdentifier detects which agent is making the request and attaches
// the agent config to the request context. Detection is based on User-Agent
// header patterns, custom X-Agent headers, or API key mapping.
func AgentIdentifier(agents map[string]config.AgentConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			agent := identifyAgent(r, agents)
			ctx := context.WithValue(r.Context(), AgentKey, agent)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func identifyAgent(r *http.Request, agents map[string]config.AgentConfig) string {
	// Check custom header first (allows agents to self-identify)
	if agentHeader := r.Header.Get("X-LLM-Gateway-Agent"); agentHeader != "" {
		if _, ok := agents[agentHeader]; ok {
			return agentHeader
		}
	}

	// Detect by User-Agent patterns
	ua := strings.ToLower(r.UserAgent())
	agentPatterns := map[string][]string{
		"claude-code": {"claude-code", "anthropic-cli"},
		"cursor":      {"cursor", "cursor-editor"},
		"aider":       {"aider"},
		"continue":    {"continue-dev"},
		"copilot":     {"github-copilot", "copilot"},
		"cline":       {"cline", "roo-code"},
	}

	for name, patterns := range agentPatterns {
		for _, pattern := range patterns {
			if strings.Contains(ua, pattern) {
				return name
			}
		}
	}

	return "unknown"
}

// MetricsRecorder wraps handlers with request/response metrics.
func MetricsRecorder(collector *metrics.Collector) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// The actual token metrics are recorded inside the proxy handler
			// after optimization. This middleware just ensures timing.
			next.ServeHTTP(w, r)
		})
	}
}

// GetAgent retrieves the identified agent from request context.
func GetAgent(ctx context.Context) string {
	agent, _ := ctx.Value(AgentKey).(string)
	if agent == "" {
		return "unknown"
	}
	return agent
}
