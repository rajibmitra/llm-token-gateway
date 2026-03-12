package metrics

import (
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Collector tracks token savings, costs, and performance across the gateway.
type Collector struct {
	// Prometheus metrics
	requestsTotal    *prometheus.CounterVec
	tokensOriginal   *prometheus.CounterVec
	tokensOptimized  *prometheus.CounterVec
	tokensSaved      *prometheus.CounterVec
	costSavedUSD     *prometheus.CounterVec
	optimizeDuration *prometheus.HistogramVec
	cacheHits        *prometheus.CounterVec
	cacheMisses      *prometheus.CounterVec
	providerLatency  *prometheus.HistogramVec

	registry *prometheus.Registry

	// In-memory aggregates for the stats endpoint
	mu          sync.RWMutex
	agentStats  map[string]*AgentStats
	totalSaved  atomic.Int64
	totalCostSaved atomic.Int64 // In microdollars for precision
}

// AgentStats holds per-agent aggregated metrics.
type AgentStats struct {
	Agent           string    `json:"agent"`
	Requests        int64     `json:"requests"`
	OriginalTokens  int64     `json:"original_tokens"`
	OptimizedTokens int64     `json:"optimized_tokens"`
	TokensSaved     int64     `json:"tokens_saved"`
	SavingsPercent  float64   `json:"savings_percent"`
	CostSavedUSD    float64   `json:"cost_saved_usd"`
	CacheHits       int64     `json:"cache_hits"`
	LastRequestAt   time.Time `json:"last_request_at"`
}

// New creates a new metrics collector with Prometheus registrations.
func New() *Collector {
	registry := prometheus.NewRegistry()

	c := &Collector{
		registry:   registry,
		agentStats: make(map[string]*AgentStats),

		requestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "llm_gateway_requests_total",
				Help: "Total number of requests processed by the gateway",
			},
			[]string{"agent", "provider", "model", "strategy"},
		),
		tokensOriginal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "llm_gateway_tokens_original_total",
				Help: "Total original (pre-optimization) tokens",
			},
			[]string{"agent", "provider", "model"},
		),
		tokensOptimized: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "llm_gateway_tokens_optimized_total",
				Help: "Total optimized tokens sent to provider",
			},
			[]string{"agent", "provider", "model"},
		),
		tokensSaved: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "llm_gateway_tokens_saved_total",
				Help: "Total tokens saved by optimization",
			},
			[]string{"agent", "provider", "model", "strategy"},
		),
		costSavedUSD: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "llm_gateway_cost_saved_usd_total",
				Help: "Estimated cost saved in USD",
			},
			[]string{"agent", "provider", "model"},
		),
		optimizeDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "llm_gateway_optimize_duration_seconds",
				Help:    "Time spent optimizing request payloads",
				Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5},
			},
			[]string{"agent", "strategy"},
		),
		cacheHits: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "llm_gateway_cache_hits_total",
				Help: "Cache hit count",
			},
			[]string{"agent", "provider"},
		),
		cacheMisses: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "llm_gateway_cache_misses_total",
				Help: "Cache miss count",
			},
			[]string{"agent", "provider"},
		),
		providerLatency: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "llm_gateway_provider_latency_seconds",
				Help:    "Latency of requests to LLM providers",
				Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60},
			},
			[]string{"provider", "model"},
		),
	}

	registry.MustRegister(
		c.requestsTotal, c.tokensOriginal, c.tokensOptimized,
		c.tokensSaved, c.costSavedUSD, c.optimizeDuration,
		c.cacheHits, c.cacheMisses, c.providerLatency,
	)

	return c
}

// RecordRequest logs a processed request with all relevant dimensions.
func (c *Collector) RecordRequest(agent, provider, model, strategy string, original, optimized int, costPerInputToken float64) {
	saved := original - optimized
	costSaved := float64(saved) * costPerInputToken

	// Prometheus counters
	c.requestsTotal.WithLabelValues(agent, provider, model, strategy).Inc()
	c.tokensOriginal.WithLabelValues(agent, provider, model).Add(float64(original))
	c.tokensOptimized.WithLabelValues(agent, provider, model).Add(float64(optimized))
	c.tokensSaved.WithLabelValues(agent, provider, model, strategy).Add(float64(saved))
	c.costSavedUSD.WithLabelValues(agent, provider, model).Add(costSaved)

	// In-memory aggregates
	c.mu.Lock()
	defer c.mu.Unlock()

	stats, ok := c.agentStats[agent]
	if !ok {
		stats = &AgentStats{Agent: agent}
		c.agentStats[agent] = stats
	}
	stats.Requests++
	stats.OriginalTokens += int64(original)
	stats.OptimizedTokens += int64(optimized)
	stats.TokensSaved += int64(saved)
	stats.CostSavedUSD += costSaved
	stats.LastRequestAt = time.Now()
	if stats.OriginalTokens > 0 {
		stats.SavingsPercent = (1 - float64(stats.OptimizedTokens)/float64(stats.OriginalTokens)) * 100
	}
}

// RecordCacheHit logs a cache hit.
func (c *Collector) RecordCacheHit(agent, provider string) {
	c.cacheHits.WithLabelValues(agent, provider).Inc()
	c.mu.Lock()
	if stats, ok := c.agentStats[agent]; ok {
		stats.CacheHits++
	}
	c.mu.Unlock()
}

// RecordCacheMiss logs a cache miss.
func (c *Collector) RecordCacheMiss(agent, provider string) {
	c.cacheMisses.WithLabelValues(agent, provider).Inc()
}

// RecordOptimizeDuration logs how long optimization took.
func (c *Collector) RecordOptimizeDuration(agent, strategy string, d time.Duration) {
	c.optimizeDuration.WithLabelValues(agent, strategy).Observe(d.Seconds())
}

// RecordProviderLatency logs provider response time.
func (c *Collector) RecordProviderLatency(provider, model string, d time.Duration) {
	c.providerLatency.WithLabelValues(provider, model).Observe(d.Seconds())
}

// GetAgentStats returns aggregated stats per agent.
func (c *Collector) GetAgentStats() map[string]*AgentStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make(map[string]*AgentStats, len(c.agentStats))
	for k, v := range c.agentStats {
		clone := *v
		result[k] = &clone
	}
	return result
}

// Handler returns the Prometheus HTTP handler.
func (c *Collector) Handler() http.Handler {
	return promhttp.HandlerFor(c.registry, promhttp.HandlerOpts{})
}
