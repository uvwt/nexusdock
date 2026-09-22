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
	"github.com/uvwt/nexusdock/internal/stage3"
	"github.com/uvwt/nexusdock/internal/workflow"
)

func Main(args []string) int {
	if err := run(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func run(args []string) error {
	// 配置加载本身 fail-fast：任何启动变量非法都会在这里终止进程，
	// admin 等本地命令也复用同一份加载结果，保证行为一致。
	cfg, err := config.LoadFromEnv()
	if err != nil {
		return fmt.Errorf("load startup configuration: %w", err)
	}
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
	// 控制库以短事务为主，不依赖多连接并发写入。保持单个 SQLite 连接，
	// 并由 OpenSQLite 使用 rollback journal，优先保证不同部署文件系统上的一致性。
	controlDB, err := core.OpenSQLite(ctx, controlDBPath, 1)
	if err != nil {
		return fmt.Errorf("open control plane database: %w", err)
	}
	defer controlDB.Close()
	if err := core.EnsureSchema(ctx, controlDB); err != nil {
		return fmt.Errorf("ensure control plane schema: %w", err)
	}
	// 启动阶段在 Schema 就绪后对控制库做一次完整 quick_check，尽早暴露磁盘或文件级损坏；
	// 运行期探针（/ready）只做轻量 SELECT 1，全库扫描不进入周期任务，避免常态负载。
	var integrity string
	if err := controlDB.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&integrity); err != nil {
		return fmt.Errorf("control plane database quick_check: %w", err)
	}
	if integrity != "ok" {
		return fmt.Errorf("control plane database quick_check reported %q", integrity)
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

	// Workflow 模板注册表以数据目录下的 published 文件为唯一事实来源，
	// 由组合根显式创建后注入 HTTP 层，REST 与集中式 MCP 工具共用同一实例。
	workflowRegistry := workflow.NewRegistry(filepath.Join(cfg.NexusDataDir, "workflow-templates"))

	// AgentDock Hub 由组合根显式创建：HTTP 连接升级、Runtime 工具调用与
	// Stage 3 进化 Worker 都依赖同一个节点连接实例。
	agentDockHub := agentdock.NewHub(agentDockNodes)

	// 节点工具契约 Bridge 持有 fleet 公开契约的业务状态（收敛、持久化、兼容性判定）；
	// 它对 MCP SDK 的发布/下架回调由 HTTP 网关在初始化时绑定。
	publishedToolBridge := agentdock.NewPublishedToolBridge(agentDockNodes, logger)

	// Artifact 签名密钥与每节点下载预算独立于 HTTP 生命周期，由组合根创建后注入下载路由。
	artifactService := agentdock.NewArtifactService(cfg.NexusDataDir)

	// Stage 3 进化分析是应用级后台任务：调度循环与快照构建属于 internal/stage3，
	// 组合根只负责创建、注入依赖并随进程生命周期启停。settings 中的 Stage 3 字段
	// 在这里映射为 stage3.WorkerConfig（stage3 不能反向依赖 settings，会构成 import cycle）。
	evolutionWorker := stage3.NewWorker(logger, func(ctx context.Context) (stage3.WorkerConfig, error) {
		aiSettings, _, err := runtimeSettings.Load(ctx)
		if err != nil {
			return stage3.WorkerConfig{}, err
		}
		return stage3.WorkerConfig{
			Enabled: aiSettings.Stage3Enabled, Endpoint: aiSettings.Stage3Endpoint, Model: aiSettings.Stage3Model,
			APIKey: aiSettings.Stage3APIKey, Timeout: aiSettings.Stage3Timeout, Interval: aiSettings.Stage3Interval,
		}, nil
	}, agentDockNodes, agentDockHub, store, workflowRegistry)

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
		httpx.WithAgentDockNodes(agentDockNodes, agentDockHub),
		httpx.WithWebAuthentication(authService),
		httpx.WithEmbeddingService(embeddingService),
		httpx.WithRuntimeSettings(runtimeSettings),
		httpx.WithRuntimeAIConfig(aiCfg),
		httpx.WithMCPSettings(mcpSettings),
		httpx.WithMCPAppsEnabled(mcpAppsEnabled),
		httpx.WithMCPTokenStore(mcpTokenStore),
		httpx.WithPrivateNotes(privateNoteStore),
		httpx.WithWorkflowRegistry(workflowRegistry),
		httpx.WithEvolutionWorker(evolutionWorker),
		httpx.WithPublishedToolBridge(publishedToolBridge),
		httpx.WithArtifactService(artifactService),
	)
	httpServer := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}

	// Worker 随信号 ctx 一起退出；HTTP 服务先优雅停机，再由 cancel 结束后台调度。
	go evolutionWorker.Run(ctx)
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
