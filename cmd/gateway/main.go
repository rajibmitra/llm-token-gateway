package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rajibmitra/llm-token-gateway/internal/cache"
	"github.com/rajibmitra/llm-token-gateway/internal/classifier"
	"github.com/rajibmitra/llm-token-gateway/internal/config"
	"github.com/rajibmitra/llm-token-gateway/internal/metrics"
	"github.com/rajibmitra/llm-token-gateway/internal/middleware"
	"github.com/rajibmitra/llm-token-gateway/internal/optimizer"
	"github.com/rajibmitra/llm-token-gateway/internal/proxy"
	"go.uber.org/zap"
)

func main() {
	configPath := flag.String("config", "configs/gateway.yaml", "path to configuration file")
	flag.Parse()

	// Initialize logger
	logger, _ := zap.NewProduction()
	defer logger.Sync()
	sugar := logger.Sugar()

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		sugar.Fatalf("failed to load config: %v", err)
	}
	sugar.Infow("configuration loaded",
		"listen_addr", cfg.Server.ListenAddr,
		"providers", len(cfg.Providers),
		"agents", len(cfg.Agents),
	)

	// Initialize components
	promptCache, err := cache.New(cfg.Cache)
	if err != nil {
		sugar.Fatalf("failed to initialize cache: %v", err)
	}
	defer promptCache.Close()

	metricsCollector := metrics.New()
	contentClassifier := classifier.New(cfg.Classifier)
	tokenOptimizer := optimizer.New(cfg.Optimizer, contentClassifier)

	// Build the middleware chain
	chain := middleware.NewChain(
		middleware.RequestLogger(logger),
		middleware.AgentIdentifier(cfg.Agents),
		middleware.MetricsRecorder(metricsCollector),
	)

	// Create the reverse proxy with all components
	gatewayProxy := proxy.New(proxy.Config{
		Providers:  cfg.Providers,
		Optimizer:  tokenOptimizer,
		Cache:      promptCache,
		Metrics:    metricsCollector,
		Classifier: contentClassifier,
		Logger:     logger,
	})

	// Setup HTTP routes
	mux := http.NewServeMux()

	// Provider API routes — agents hit these as if talking to the real API
	mux.Handle("/v1/messages", chain.Then(gatewayProxy.HandleAnthropic()))
	mux.Handle("/v1/chat/completions", chain.Then(gatewayProxy.HandleOpenAI()))
	mux.Handle("/v1beta/models/", chain.Then(gatewayProxy.HandleGemini()))

	// Gateway management endpoints
	mux.Handle("/gateway/health", http.HandlerFunc(healthCheck))
	mux.Handle("/gateway/metrics", metricsCollector.Handler())
	mux.Handle("/gateway/stats", gatewayProxy.StatsHandler())
	mux.Handle("/gateway/cache/flush", chain.Then(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		promptCache.Flush(r.Context())
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"flushed"}`)
	})))

	// Start the server
	server := &http.Server{
		Addr:         cfg.Server.ListenAddr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 300 * time.Second, // Long timeout for streaming responses
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh

		sugar.Info("shutting down gateway...")
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			sugar.Errorf("forced shutdown: %v", err)
		}
	}()

	sugar.Infow("llm-token-gateway starting", "addr", cfg.Server.ListenAddr)
	if cfg.Server.TLS.Enabled {
		err = server.ListenAndServeTLS(cfg.Server.TLS.CertFile, cfg.Server.TLS.KeyFile)
	} else {
		err = server.ListenAndServe()
	}
	if err != nil && err != http.ErrServerClosed {
		sugar.Fatalf("server error: %v", err)
	}
}

func healthCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"status":"ok","version":"0.1.0"}`)
}
