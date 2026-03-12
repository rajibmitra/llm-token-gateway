package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/rajibmitra/llm-token-gateway/internal/cache"
	"github.com/rajibmitra/llm-token-gateway/internal/classifier"
	"github.com/rajibmitra/llm-token-gateway/internal/config"
	"github.com/rajibmitra/llm-token-gateway/internal/metrics"
	"github.com/rajibmitra/llm-token-gateway/internal/middleware"
	"github.com/rajibmitra/llm-token-gateway/internal/optimizer"
	"go.uber.org/zap"
)

// Config holds all dependencies for the gateway proxy.
type Config struct {
	Providers  map[string]config.ProviderConfig
	Optimizer  *optimizer.Optimizer
	Cache      *cache.Cache
	Metrics    *metrics.Collector
	Classifier *classifier.Classifier
	Logger     *zap.Logger
}

// Gateway is the core reverse proxy that handles all LLM API traffic.
type Gateway struct {
	cfg       Config
	providers map[string]*httputil.ReverseProxy
	logger    *zap.SugaredLogger
}

// New creates a new gateway proxy.
func New(cfg Config) *Gateway {
	g := &Gateway{
		cfg:       cfg,
		providers: make(map[string]*httputil.ReverseProxy),
		logger:    cfg.Logger.Sugar(),
	}

	// Initialize reverse proxies for each provider
	for name, provCfg := range cfg.Providers {
		target, err := url.Parse(provCfg.BaseURL)
		if err != nil {
			g.logger.Fatalf("invalid provider URL %s: %v", name, err)
		}

		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.Transport = &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 20,
			IdleConnTimeout:     90 * time.Second,
		}

		// Customize the Director to inject auth and headers
		originalDirector := proxy.Director
		apiKey := os.Getenv(provCfg.APIKeyEnv)
		proxy.Director = func(req *http.Request) {
			originalDirector(req)
			req.Host = target.Host

			// Inject provider-specific auth
			switch name {
			case "anthropic":
				req.Header.Set("x-api-key", apiKey)
				req.Header.Set("anthropic-version", "2023-06-01")
			case "openai":
				req.Header.Set("Authorization", "Bearer "+apiKey)
			case "gemini":
				// Gemini uses query param or header
				q := req.URL.Query()
				q.Set("key", apiKey)
				req.URL.RawQuery = q.Encode()
			}

			// Apply custom headers from config
			for k, v := range provCfg.Headers {
				req.Header.Set(k, v)
			}
		}

		// Customize error handler
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			g.logger.Errorw("proxy error", "provider", name, "error", err)
			http.Error(w, fmt.Sprintf(`{"error":{"type":"gateway_error","message":"%s"}}`, err.Error()),
				http.StatusBadGateway)
		}

		g.providers[name] = proxy
	}

	return g
}

// HandleAnthropic returns a handler for Anthropic's /v1/messages endpoint.
func (g *Gateway) HandleAnthropic() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.handleRequest(w, r, "anthropic", "anthropic")
	})
}

// HandleOpenAI returns a handler for OpenAI's /v1/chat/completions endpoint.
func (g *Gateway) HandleOpenAI() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.handleRequest(w, r, "openai", "openai")
	})
}

// HandleGemini returns a handler for Google's Gemini API endpoints.
func (g *Gateway) HandleGemini() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.handleRequest(w, r, "gemini", "gemini")
	})
}

// handleRequest is the core request processing pipeline.
func (g *Gateway) handleRequest(w http.ResponseWriter, r *http.Request, providerName, providerType string) {
	agent := middleware.GetAgent(r.Context())
	start := time.Now()

	// Read the request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		g.logger.Errorw("failed to read request body", "error", err)
		http.Error(w, `{"error":"failed to read request body"}`, http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Parse the request to extract messages and model
	model, messages, isStreaming, err := g.parseRequest(body, providerType)
	if err != nil {
		g.logger.Errorw("failed to parse request", "error", err)
		http.Error(w, `{"error":"failed to parse request"}`, http.StatusBadRequest)
		return
	}

	// --- OPTIMIZATION PIPELINE ---

	// Step 1: Check prompt cache (non-streaming only)
	if !isStreaming {
		cacheKey := cache.HashKey(model, body)
		entry, err := g.cfg.Cache.Get(r.Context(), cacheKey)
		if err == nil && entry != nil {
			g.logger.Infow("cache hit", "agent", agent, "model", model)
			g.cfg.Metrics.RecordCacheHit(agent, providerName)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-LLM-Gateway-Cache", "HIT")
			w.Write(entry.Response)
			return
		}
		g.cfg.Metrics.RecordCacheMiss(agent, providerName)
	}

	// Step 2: Optimize messages
	optimizeStart := time.Now()
	optimizedMessages, report := g.cfg.Optimizer.OptimizeMessages(messages)
	optimizeDuration := time.Since(optimizeStart)

	strategy := "passthrough"
	if len(report.Strategies) > 0 {
		strategy = strings.Join(report.Strategies, "+")
	}
	g.cfg.Metrics.RecordOptimizeDuration(agent, strategy, optimizeDuration)

	g.logger.Infow("optimization complete",
		"agent", agent,
		"model", model,
		"original_chars", report.TotalOriginalChars,
		"optimized_chars", report.TotalOptimizedChars,
		"savings_percent", fmt.Sprintf("%.1f%%", report.SavingsPercent()),
		"strategies", report.Strategies,
		"duration", optimizeDuration,
	)

	// Step 3: Rebuild the request body with optimized messages
	optimizedBody, err := g.rebuildRequest(body, optimizedMessages, providerType)
	if err != nil {
		g.logger.Errorw("failed to rebuild request", "error", err)
		// Fall back to original body
		optimizedBody = body
	}

	// Step 4: Record metrics
	// Rough token estimate: 1 token ≈ 4 chars (varies by tokenizer)
	originalTokens := report.TotalOriginalChars / 4
	optimizedTokens := report.TotalOptimizedChars / 4
	costPerToken := g.getCostPerInputToken(providerName, model)
	g.cfg.Metrics.RecordRequest(agent, providerName, model, strategy,
		originalTokens, optimizedTokens, costPerToken)

	// Step 5: Forward to provider
	r.Body = io.NopCloser(bytes.NewReader(optimizedBody))
	r.ContentLength = int64(len(optimizedBody))
	r.Header.Set("Content-Length", fmt.Sprintf("%d", len(optimizedBody)))

	// Add gateway headers for observability
	w.Header().Set("X-LLM-Gateway-Agent", agent)
	w.Header().Set("X-LLM-Gateway-Strategy", strategy)
	w.Header().Set("X-LLM-Gateway-Savings", fmt.Sprintf("%.1f%%", report.SavingsPercent()))

	// Forward with timing — capture response to store in cache
	proxyStart := time.Now()
	if !isStreaming {
		rec := &responseRecorder{ResponseWriter: w, body: &bytes.Buffer{}}
		g.providers[providerName].ServeHTTP(rec, r)
		g.cfg.Metrics.RecordProviderLatency(providerName, model, time.Since(proxyStart))

		// Cache the response on success
		if rec.status == 0 || rec.status == http.StatusOK {
			cacheKey := cache.HashKey(model, body)
			tokensSaved := (report.TotalOriginalChars - report.TotalOptimizedChars) / 4
			_ = g.cfg.Cache.Set(r.Context(), cacheKey, rec.body.Bytes(), providerName, model, tokensSaved, 0)
		}
	} else {
		g.providers[providerName].ServeHTTP(w, r)
		g.cfg.Metrics.RecordProviderLatency(providerName, model, time.Since(proxyStart))
	}

	g.logger.Infow("request forwarded",
		"agent", agent,
		"provider", providerName,
		"model", model,
		"total_duration", time.Since(start),
	)
}

// parseRequest extracts model, messages, and streaming flag from the request body.
func (g *Gateway) parseRequest(body []byte, providerType string) (string, []optimizer.Message, bool, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return "", nil, false, fmt.Errorf("invalid JSON body: %w", err)
	}

	// Extract model
	var model string
	if m, ok := raw["model"]; ok {
		json.Unmarshal(m, &model)
	}

	// Extract streaming flag
	var streaming bool
	if s, ok := raw["stream"]; ok {
		json.Unmarshal(s, &streaming)
	}

	// Extract messages
	var messages []optimizer.Message
	if m, ok := raw["messages"]; ok {
		if err := json.Unmarshal(m, &messages); err != nil {
			return model, nil, streaming, fmt.Errorf("invalid messages: %w", err)
		}
	}

	// For Anthropic: also check the system prompt
	if providerType == "anthropic" {
		if sys, ok := raw["system"]; ok {
			var systemContent string
			if err := json.Unmarshal(sys, &systemContent); err == nil {
				// Prepend system as a virtual message for optimization
				messages = append([]optimizer.Message{
					{Role: "system", Content: systemContent},
				}, messages...)
			}
		}
	}

	return model, messages, streaming, nil
}

// rebuildRequest puts the optimized messages back into the request body.
func (g *Gateway) rebuildRequest(originalBody []byte, messages []optimizer.Message, providerType string) ([]byte, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(originalBody, &raw); err != nil {
		return nil, err
	}

	// Handle system message for Anthropic
	if providerType == "anthropic" && len(messages) > 0 && messages[0].Role == "system" {
		systemJSON, err := json.Marshal(messages[0].Content)
		if err == nil {
			raw["system"] = systemJSON
		}
		messages = messages[1:]
	}

	// Replace messages
	messagesJSON, err := json.Marshal(messages)
	if err != nil {
		return nil, err
	}
	raw["messages"] = messagesJSON

	return json.Marshal(raw)
}

// getCostPerInputToken returns the cost per input token for a given model.
// These are approximate costs per token in USD as of early 2026.
func (g *Gateway) getCostPerInputToken(provider, model string) float64 {
	// Cost per input token (USD)
	costs := map[string]float64{
		// Anthropic
		"claude-opus-4-6":         0.000015,
		"claude-sonnet-4-6":       0.000003,
		"claude-haiku-4-5":        0.0000008,
		// OpenAI
		"gpt-4o":                  0.0000025,
		"gpt-4o-mini":             0.00000015,
		"o3":                      0.00001,
		// Google
		"gemini-2.5-pro":          0.00000125,
		"gemini-2.5-flash":        0.000000075,
	}

	if cost, ok := costs[model]; ok {
		return cost
	}
	// Default fallback
	return 0.000003
}

// responseRecorder captures the response body so it can be stored in cache.
type responseRecorder struct {
	http.ResponseWriter
	body   *bytes.Buffer
	status int
}

func (r *responseRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	r.body.Write(b)
	return r.ResponseWriter.Write(b)
}

// StatsHandler returns an HTTP handler that serves gateway statistics as JSON.
func (g *Gateway) StatsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stats := g.cfg.Metrics.GetAgentStats()
		cacheStats := g.cfg.Cache.Stats(context.Background())

		response := map[string]interface{}{
			"agents": stats,
			"cache": map[string]interface{}{
				"hit_rate":   fmt.Sprintf("%.1f%%", cacheStats.HitRate*100),
				"hits":       cacheStats.Hits,
				"misses":     cacheStats.Misses,
				"entries":    cacheStats.Entries,
				"size_bytes": cacheStats.SizeBytes,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})
}
