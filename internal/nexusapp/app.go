package nexusapp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/uvwt/nexusdock/internal/agentdock"
	"github.com/uvwt/nexusdock/internal/auth"
	"github.com/uvwt/nexusdock/internal/config"
	"github.com/uvwt/nexusdock/internal/core"
	"github.com/uvwt/nexusdock/internal/httpx"
	"github.com/uvwt/nexusdock/internal/privatenotes"
	"github.com/uvwt/nexusdock/internal/recall"
	"github.com/uvwt/nexusdock/internal/settings"
)

func Main(args []string) int {
	if err := run(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func run(args []string) error {
	cfg := config.FromEnv()
	if adminCommandRequested(args) {
		return runAdminCommand(context.Background(), cfg, args)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel()}))
	slog.SetDefault(logger)

	if err := cfg.ValidateStartup(); err != nil {
		return fmt.Errorf("invalid startup configuration: %w", err)
	}

	store, err := recall.NewStore(cfg.RecallRepoDir)
	if err != nil {
		return fmt.Errorf("initialize recall store: %w", err)
	}
	privateNoteStore, err := privatenotes.New(filepath.Join(cfg.RecallRepoDir, "private-notes"))
	if err != nil {
		return fmt.Errorf("initialize private notes: %w", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	controlDir := cfg.NexusDataDir
	if err := os.MkdirAll(controlDir, 0o700); err != nil {
		return fmt.Errorf("create control plane directory: %w", err)
	}
	controlDBPath := filepath.Join(controlDir, "nexus.db")
	// 控制库事务都很短，生产又运行在 macOS 外置卷的 Docker bind mount 上。
	// 保持单个 SQLite 连接，并由 OpenSQLite 使用 rollback journal，降低跨宿主文件系统的一致性风险。
	controlDB, err := core.OpenSQLite(ctx, controlDBPath, 1)
	if err != nil {
		return fmt.Errorf("open control plane database: %w", err)
	}
	defer controlDB.Close()
	if err := core.EnsureSchema(ctx, controlDB); err != nil {
		return fmt.Errorf("ensure control plane schema: %w", err)
	}
	runtimeSettings, err := settings.NewStore(controlDB, controlDir)
	if err != nil {
		return fmt.Errorf("initialize runtime AI settings: %w", err)
	}
	aiCfg, _, err := runtimeSettings.Load(ctx)
	if err != nil {
		return fmt.Errorf("load runtime AI settings: %w", err)
	}
	mcpSettings, err := settings.NewMCPStore(controlDB)
	if err != nil {
		return fmt.Errorf("initialize MCP settings: %w", err)
	}
	mcpAppsEnabled, _, err := mcpSettings.Load(ctx)
	if err != nil {
		return fmt.Errorf("load MCP settings: %w", err)
	}
	agentDockNodes, err := agentdock.NewStore(controlDB)
	if err != nil {
		return fmt.Errorf("initialize AgentDock node store: %w", err)
	}

	mcpTokenStore, err := auth.NewMCPTokenStore(controlDir)
	if err != nil {
		return fmt.Errorf("initialize MCP access token: %w", err)
	}

	authService := auth.NewService(controlDB)
	status, err := authService.AdminStatus(ctx)
	if err != nil {
		return fmt.Errorf("read administrator status: %w", err)
	}
	if !status.Initialized {
		logger.Warn("administrator is not initialized; run the local admin init command")
	}

	embeddingService := recall.NewEmbeddingService(store, recall.EmbeddingConfig{
		Enabled: aiCfg.EmbeddingEnabled, Endpoint: aiCfg.EmbeddingEndpoint, Model: aiCfg.EmbeddingModel, APIKey: aiCfg.EmbeddingAPIKey,
		Timeout: aiCfg.EmbeddingTimeout,
	})

	server := httpx.NewServer(
		cfg,
		store,
		logger,
		httpx.WithSystemDatabase(controlDB),
		httpx.WithAgentDockNodes(agentDockNodes),
		httpx.WithWebAuthentication(authService),
		httpx.WithEmbeddingService(embeddingService),
		httpx.WithRuntimeSettings(runtimeSettings),
		httpx.WithRuntimeAIConfig(aiCfg),
		httpx.WithMCPSettings(mcpSettings),
		httpx.WithMCPAppsEnabled(mcpAppsEnabled),
		httpx.WithMCPTokenStore(mcpTokenStore),
		httpx.WithPrivateNotes(privateNoteStore),
	)
	httpServer := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}

	server.StartEvolutionStage3(ctx)
	logger.Info("nexusdock starting", "addr", cfg.Addr(), "nexus_data_dir", cfg.NexusDataDir, "recall_repo_dir", cfg.RecallRepoDir, "mcp_apps_enabled", mcpAppsEnabled, "embedding_enabled", aiCfg.EmbeddingEnabled, "embedding_model", aiCfg.EmbeddingModel, "stage3_evolution_enabled", aiCfg.Stage3Enabled && aiCfg.Stage3Endpoint != "" && aiCfg.Stage3Model != "")
	serveErr := serveHTTP(ctx, httpServer)
	cancel()
	if serveErr != nil {
		return serveErr
	}
	logger.Info("nexusdock stopped")
	return nil
}

func serveHTTP(ctx context.Context, server *http.Server) error {
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", server.Addr, err)
	}
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()

	select {
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return fmt.Errorf("shutdown HTTP server: %w", err)
		}
		if err := <-serveErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP during shutdown: %w", err)
		}
		return nil
	}
}
