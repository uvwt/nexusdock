package httpx

import (
	"context"

	"github.com/uvwt/nexusdock/internal/recall"
)

// executeRecallSearch 统一 REST 与 MCP 的 Recall 搜索策略：配置 Embedding 时走 hybrid search，
// 否则回退到 Store 的本地搜索。其余单 Store 操作由各协议边界直接调用 Store，避免机械代理。
func (s *Server) executeRecallSearch(ctx context.Context, options recall.SearchOptions) ([]recall.SearchResult, error) {
	embedding := s.runtimeAI.currentEmbedding()
	if embedding == nil {
		return s.store.SearchWithOptions(options)
	}
	return embedding.HybridSearch(ctx, options)
}
