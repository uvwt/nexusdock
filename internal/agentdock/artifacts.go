package agentdock

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	protocol "github.com/uvwt/agentdock-protocol"
)

// ArtifactChunk 是 AgentDock 私有 artifact.read Bridge 操作在 Nexus 边界内的强类型结果。
type ArtifactChunk struct {
	ArtifactID string    `json:"artifact_id"`
	Filename   string    `json:"filename"`
	MIMEType   string    `json:"mime_type"`
	Size       int64     `json:"size_bytes"`
	SHA256     string    `json:"sha256"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Archive    bool      `json:"archive"`
	Width      int       `json:"width,omitempty"`
	Height     int       `json:"height,omitempty"`
	Offset     int64     `json:"offset"`
	NextOffset int64     `json:"next_offset"`
	DataBase64 string    `json:"data_base64"`
	EOF        bool      `json:"eof"`
}

func (h *Hub) ReadArtifactChunk(ctx context.Context, nodeID, artifactID string, offset int64, maxBytes int) (ArtifactChunk, error) {
	result, err := h.Invoke(ctx, nodeID, protocol.OperationArtifactRead, map[string]any{
		"artifact_id": artifactID,
		"offset":      offset,
		"max_bytes":   maxBytes,
	})
	if err != nil {
		return ArtifactChunk{}, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return ArtifactChunk{}, fmt.Errorf("编码 AgentDock Artifact 分块结果: %w", err)
	}
	var chunk ArtifactChunk
	if err := json.Unmarshal(encoded, &chunk); err != nil {
		return ArtifactChunk{}, fmt.Errorf("解析 AgentDock Artifact 分块结果: %w", err)
	}
	return chunk, nil
}
