// Package main provides the entry point for the rate limiter service.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/nshekhawat/rate-limiter-go/internal/config"
	"github.com/nshekhawat/rate-limiter-go/internal/metrics"
	"github.com/nshekhawat/rate-limiter-go/internal/ratelimiter"
	"github.com/nshekhawat/rate-limiter-go/internal/server"
	"github.com/nshekhawat/rate-limiter-go/internal/storage"
)

func main() {
	// Get config path from env or use default
	configPath := os.Getenv("CONFIG_PATH")

	// Load configuration
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	logger, err := initLogger(cfg.Logging)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = logger.Sync() }()

	logger.Info("starting rate limiter service",
		zap.String("version", "1.0.0"),
	)

	// Initialize storage
	store, err := initStorage(cfg, logger)
	if err != nil {
		logger.Fatal("failed to initialize storage", zap.Error(err))
	}
	defer func() { _ = store.Close() }()

	// Verify storage connectivity
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := store.Ping(ctx); err != nil {
		logger.Fatal("storage ping failed", zap.Error(err))
	}
	cancel()
	logger.Info("storage connected successfully")

	// Initialize rate limiter using the config helper
	limiterConfig := cfg.ToRateLimiterConfig()
	limiter := ratelimiter.NewRateLimiter(store, limiterConfig, logger)

	// Start servers
	errCh := make(chan error, 2)

	// Start HTTP server
	httpConfig := &server.HTTPConfig{
		Port:           cfg.Server.HTTPPort,
		ReadTimeout:    cfg.Server.ReadTimeout,
		WriteTimeout:   cfg.Server.WriteTimeout,
		MetricsEnabled: cfg.Metrics.Enabled,
		MetricsPath:    cfg.Metrics.Path,
	}
	httpServer := server.NewHTTPServer(limiter, httpConfig, logger)

	go func() {
		logger.Info("starting HTTP server", zap.Int("port", cfg.Server.HTTPPort))
		if err := httpServer.Start(); err != nil {
			errCh <- fmt.Errorf("HTTP server error: %w", err)
		}
	}()

	// Start gRPC server
	grpcConfig := &server.GRPCConfig{
		Port:              cfg.Server.GRPCPort,
		MaxRecvMsgSize:    4 * 1024 * 1024,
		MaxSendMsgSize:    4 * 1024 * 1024,
		EnableReflection:  true,
		EnableHealthCheck: true,
	}
	grpcServer := server.NewGRPCServer(limiter, grpcConfig, logger)

	go func() {
		logger.Info("starting gRPC server", zap.Int("port", cfg.Server.GRPCPort))
		if err := grpcServer.Start(); err != nil {
			errCh <- fmt.Errorf("gRPC server error: %w", err)
		}
	}()

	// Wait for shutdown signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		logger.Error("server error", zap.Error(err))
	case sig := <-quit:
		logger.Info("received shutdown signal", zap.String("signal", sig.String()))
	}

	// Graceful shutdown
	logger.Info("shutting down servers...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// Stop HTTP server
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown error", zap.Error(err))
	}

	// Stop gRPC server
	grpcServer.Stop()

	logger.Info("servers stopped successfully")
}

func initLogger(cfg config.LoggingConfig) (*zap.Logger, error) {
	var level zapcore.Level
	if err := level.UnmarshalText([]byte(cfg.Level)); err != nil {
		level = zapcore.InfoLevel
	}

	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "timestamp"
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	zapConfig := zap.Config{
		Level:            zap.NewAtomicLevelAt(level),
		Development:      false,
		Encoding:         cfg.Format,
		EncoderConfig:    encoderConfig,
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
	}

	return zapConfig.Build()
}

func initStorage(cfg *config.Config, logger *zap.Logger) (storage.AtomicStorage, error) {
	// Check if Redis is configured (non-empty address indicates Redis should be used)
	useRedis := os.Getenv("RATE_LIMITER_USE_REDIS") == "true"

	if useRedis {
		logger.Info("initializing Redis storage",
			zap.String("address", cfg.Redis.Address),
		)
		redisConfig := &storage.RedisConfig{
			Address:      cfg.Redis.Address,
			Password:     cfg.Redis.Password,
			DB:           cfg.Redis.DB,
			PoolSize:     cfg.Redis.PoolSize,
			MinIdleConns: cfg.Redis.MinIdleConns,
			DialTimeout:  cfg.Redis.DialTimeout,
			ReadTimeout:  cfg.Redis.ReadTimeout,
			WriteTimeout: cfg.Redis.WriteTimeout,
		}
		store, err := storage.NewRedisStorage(redisConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create Redis storage: %w", err)
		}
		// Wrap with instrumented storage
		return metrics.NewInstrumentedAtomicStorage(store, "redis", nil), nil
	}

	logger.Info("initializing memory storage")
	store := storage.NewMemoryStorage(cfg.RateLimit.TTL)
	return metrics.NewInstrumentedAtomicStorage(store, "memory", nil), nil
}
