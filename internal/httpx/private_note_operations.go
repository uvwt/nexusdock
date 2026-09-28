package httpx

import (
	"context"
	"strings"

	"github.com/uvwt/nexusdock/internal/privatenotes"
)

type privateNoteSearchRequest struct {
	Query      string
	MaxResults int
}

type privateNoteSearchResult struct {
	Action       string                 `json:"action"`
	Query        string                 `json:"query"`
	Root         string                 `json:"root"`
	Results      []privatenotes.Summary `json:"results"`
	Count        int                    `json:"count"`
	MetadataOnly bool                   `json:"metadata_only"`
}

// executePrivateNoteSearch 是 REST 与 MCP 共用的搜索结果编排：Store 负责查询领域规则，
// 这里补充只读元数据输出；Read/Write/Delete/Status/Maintain 直接调用 Store，避免空心代理。
func (s *Server) executePrivateNoteSearch(ctx context.Context, req privateNoteSearchRequest) (privateNoteSearchResult, error) {
	query := strings.TrimSpace(req.Query)
	results, err := s.privateNotes.Search(ctx, query, req.MaxResults)
	if err != nil {
		return privateNoteSearchResult{}, err
	}
	return privateNoteSearchResult{
		Action: "search", Query: query, Root: s.privateNotes.Root(),
		Results: results, Count: len(results), MetadataOnly: true,
	}, nil
}
