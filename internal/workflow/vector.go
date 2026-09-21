package workflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/uvwt/nexusdock/internal/recall"
)

// maxEmbeddingResponseBytes 限制 embedding 响应体大小，防止异常上游耗尽内存。
const maxEmbeddingResponseBytes = 32 << 20

// errVectorIndexStale 表示索引文件的 embedding 模型或 Registry generation 已过期；
// 状态报告按 stale 处理（提示重建），而不是当成索引损坏。
var errVectorIndexStale = errors.New("workflow vector index is stale")

// errRegistryChangedDuringReindex 表示 embedding 网络调用期间模板集合发生了变化。
// 旧快照生成的索引不能覆盖到新一代 Registry 上，调用方应重新触发重建。
var errRegistryChangedDuringReindex = errors.New("workflow registry changed while vector index was rebuilding")

// 向量索引状态；这些值会原样出现在 HTTP/MCP 响应的 vector_index_status 字段里。
const (
	VectorIndexNotConfigured = "not_configured"
	VectorIndexMissing       = "missing"
	VectorIndexStale         = "stale"
	VectorIndexInvalid       = "invalid"
	VectorIndexReady         = "ready"
)

// AIConfig 是 match 与向量索引需要的运行时 embedding 配置子集。
// AI 设置可以在运行期通过设置页修改，因此由 HTTP/MCP 边界在每次调用时
// 从 settings.RuntimeAIConfig 映射传入，Registry 本身不持有可变配置。
type AIConfig struct {
	Enabled  bool
	Endpoint string
	Model    string
	APIKey   string
	Timeout  time.Duration
}

// VectorEnabled 判断向量检索是否可用：必须显式开启且配置了 endpoint。
func (c AIConfig) VectorEnabled() bool {
	return c.Enabled && strings.TrimSpace(c.Endpoint) != ""
}

// VectorIndex 是 published 目录旁 vector-index.json 的完整结构，
// key 形如 "<id>@<version>"；文件与模板一样使用严格 JSON 解码。
type VectorIndex struct {
	Model      string                    `json:"model"`
	Generation string                    `json:"generation"`
	Dimension  int                       `json:"dimension,omitempty"`
	UpdatedAt  time.Time                 `json:"updated_at"`
	Documents  map[string]VectorDocument `json:"documents"`
}

type VectorDocument struct {
	ID        string    `json:"id"`
	Version   string    `json:"version"`
	Hash      string    `json:"hash"`
	Text      string    `json:"text"`
	Vector    []float64 `json:"vector"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ReindexResult 是一次向量重建的结果摘要，供边界组装响应。
type ReindexResult struct {
	Count     int
	Model     string
	Dimension int
	IndexPath string
}

// VectorIndexSnapshot 是向量索引文件在某一时刻的状态与内容快照。
// Content/SizeBytes/ModTime 只在 ready 时有效，供 vector_index 查询直接展示。
type VectorIndexSnapshot struct {
	Status    string
	Model     string
	Items     int
	Dimension int
	Content   []byte
	SizeBytes int64
	ModTime   time.Time
}

// VectorIndexInfo 返回向量索引状态与文档数；索引损坏报告为 invalid 而不是报错，
// 因为 match 主流程在索引不可用时会退回纯词法打分，状态只是观测信息。
func (r *Registry) VectorIndexInfo(ai AIConfig) (string, int) {
	if !ai.VectorEnabled() {
		return VectorIndexNotConfigured, 0
	}
	r.mu.Lock()
	_, generation, generationErr := r.activeTemplatesAndGenerationLocked()
	var idx VectorIndex
	var err error
	if generationErr == nil {
		idx, err = r.loadVectorIndex(ai.Model, generation)
	}
	r.mu.Unlock()
	if generationErr != nil {
		return VectorIndexInvalid, 0
	}
	if err != nil {
		switch {
		case errors.Is(err, os.ErrNotExist):
			return VectorIndexMissing, 0
		case errors.Is(err, errVectorIndexStale):
			return VectorIndexStale, 0
		}
		return VectorIndexInvalid, 0
	}
	return VectorIndexReady, len(idx.Documents)
}

// VectorIndexSnapshot 读取向量索引文件供 HTTP/MCP 的 vector_index 查询展示。
// 读文件失败按 missing 处理、模型不匹配按 stale 处理；只有文件内容本身
// 无法解析/校验才返回错误，由边界映射成 WORKFLOW_VECTOR_INDEX_INVALID。
func (r *Registry) VectorIndexSnapshot(ai AIConfig) (VectorIndexSnapshot, error) {
	if !ai.VectorEnabled() {
		return VectorIndexSnapshot{Status: VectorIndexNotConfigured}, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, generation, err := r.activeTemplatesAndGenerationLocked()
	if err != nil {
		return VectorIndexSnapshot{}, err
	}
	path := r.vectorIndexPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return VectorIndexSnapshot{Status: VectorIndexMissing}, nil
	}
	idx, err := decodeVectorIndex(data, ai.Model, generation)
	if errors.Is(err, errVectorIndexStale) {
		return VectorIndexSnapshot{Status: VectorIndexStale, Model: ai.Model}, nil
	}
	if err != nil {
		return VectorIndexSnapshot{}, err
	}
	snapshot := VectorIndexSnapshot{Status: VectorIndexReady, Model: idx.Model, Items: len(idx.Documents), Dimension: idx.Dimension, Content: data}
	if info, statErr := os.Stat(path); statErr == nil {
		snapshot.SizeBytes = info.Size()
		snapshot.ModTime = info.ModTime().UTC()
	}
	return snapshot, nil
}

// ReindexVectors 对全部 active 模板重建向量索引：唯一写入口，
// 校验不过或 embedding 结果不一致时不落盘，保持旧索引可用。
func (r *Registry) ReindexVectors(ctx context.Context, ai AIConfig) (ReindexResult, error) {
	if !ai.VectorEnabled() {
		return ReindexResult{}, errors.New("workflow template vector search is disabled; configure and enable vector search")
	}
	r.mu.Lock()
	templates, generation, err := r.activeTemplatesAndGenerationLocked()
	r.mu.Unlock()
	if err != nil {
		return ReindexResult{}, err
	}
	texts := make([]string, 0, len(templates))
	for _, t := range templates {
		texts = append(texts, vectorText(t))
	}
	vectors, err := embedTexts(ctx, ai, texts)
	if err != nil {
		return ReindexResult{}, err
	}
	if len(vectors) != len(templates) {
		return ReindexResult{}, fmt.Errorf("embedding response count mismatch: got %d want %d", len(vectors), len(templates))
	}
	idx := VectorIndex{Model: ai.Model, Generation: generation, UpdatedAt: time.Now().UTC(), Documents: map[string]VectorDocument{}}
	if len(vectors) > 0 {
		idx.Dimension = len(vectors[0])
	}
	for i, t := range templates {
		if len(vectors[i]) != idx.Dimension {
			return ReindexResult{}, fmt.Errorf("embedding dimension mismatch at result %d: got %d want %d", i, len(vectors[i]), idx.Dimension)
		}
		key := t.ID + "@" + t.Version
		idx.Documents[key] = VectorDocument{ID: t.ID, Version: t.Version, Hash: templateHash(t), Text: texts[i], Vector: vectors[i], UpdatedAt: time.Now().UTC()}
	}
	if err := validateVectorIndex(idx, ai.Model); err != nil {
		return ReindexResult{}, err
	}
	// embedding 网络调用期间不持锁；落盘前必须确认 active 模板 generation 没变，
	// 否则旧快照会覆盖到新一代 Registry 上。
	r.mu.Lock()
	defer r.mu.Unlock()
	_, currentGeneration, err := r.activeTemplatesAndGenerationLocked()
	if err != nil {
		return ReindexResult{}, err
	}
	if currentGeneration != generation {
		return ReindexResult{}, errRegistryChangedDuringReindex
	}
	if err := writeTemplateJSON(r.vectorIndexPath(), idx); err != nil {
		return ReindexResult{}, err
	}
	return ReindexResult{Count: len(idx.Documents), Model: ai.Model, Dimension: idx.Dimension, IndexPath: r.vectorIndexPath()}, nil
}

func (r *Registry) vectorIndexPath() string {
	return filepath.Join(r.root, "vector-index.json")
}

func (r *Registry) loadVectorIndex(model, generation string) (VectorIndex, error) {
	data, err := os.ReadFile(r.vectorIndexPath())
	if err != nil {
		return VectorIndex{}, err
	}
	return decodeVectorIndex(data, model, generation)
}

// decodeVectorIndex 使用严格 JSON 解码并立即校验，坏文件不会带出半可用索引。
// 旧版索引没有 generation，升级后会自然报告 stale 并等待重建，而不是误报损坏。
func decodeVectorIndex(data []byte, model, generation string) (VectorIndex, error) {
	var idx VectorIndex
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&idx); err != nil {
		return VectorIndex{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return VectorIndex{}, errors.New("workflow vector index contains multiple JSON values")
		}
		return VectorIndex{}, fmt.Errorf("read trailing workflow vector index data: %w", err)
	}
	if err := validateVectorIndex(idx, model); err != nil {
		return VectorIndex{}, err
	}
	if strings.TrimSpace(idx.Generation) == "" || (generation != "" && idx.Generation != generation) {
		return VectorIndex{}, fmt.Errorf("%w: registry generation does not match", errVectorIndexStale)
	}
	return idx, nil
}

func validateVectorIndex(idx VectorIndex, model string) error {
	if strings.TrimSpace(idx.Model) == "" {
		return errors.New("workflow vector index model is empty")
	}
	if strings.TrimSpace(model) != "" && idx.Model != model {
		return fmt.Errorf("%w: model %q does not match %q", errVectorIndexStale, idx.Model, model)
	}
	if idx.Documents == nil {
		return errors.New("workflow vector index documents are missing")
	}
	if len(idx.Documents) == 0 {
		if idx.Dimension != 0 {
			return errors.New("empty workflow vector index must have dimension 0")
		}
		return nil
	}
	if idx.Dimension <= 0 {
		return errors.New("workflow vector index dimension must be positive")
	}
	for key, document := range idx.Documents {
		if document.ID == "" || document.Version == "" || key != document.ID+"@"+document.Version {
			return fmt.Errorf("workflow vector index document key mismatch for %q", key)
		}
		if len(document.Vector) != idx.Dimension {
			return fmt.Errorf("workflow vector index document %q has dimension %d, want %d", key, len(document.Vector), idx.Dimension)
		}
	}
	return nil
}

// activeTemplatesAndGenerationLocked 返回当前参与 match 的模板集合及其 generation。
// 调用方必须持有 r.mu；generation 只取每个 ID 的最新 active 版本，并把模板内容哈希
// 纳入摘要，因此发布、退役或文件内容变化都会让既有向量索引立即变成 stale。
func (r *Registry) activeTemplatesAndGenerationLocked() ([]Template, string, error) {
	templates, err := r.listLocked(StatusActive)
	if err != nil {
		return nil, "", err
	}
	templates = LatestVersions(templates)
	return templates, templateGeneration(templates), nil
}

func templateGeneration(templates []Template) string {
	hash := sha256.New()
	for _, template := range templates {
		_, _ = io.WriteString(hash, template.ID)
		_, _ = io.WriteString(hash, "\x00")
		_, _ = io.WriteString(hash, template.Version)
		_, _ = io.WriteString(hash, "\x00")
		_, _ = io.WriteString(hash, templateHash(template))
		_, _ = io.WriteString(hash, "\n")
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

// vectorScores 用当前目标文本查询 embedding 端点并对索引文档做余弦打分。
// 任一环节失败都返回 nil：match 退回纯词法打分，向量检索只是加分项，不阻断主流程。
func vectorScores(ctx context.Context, ai AIConfig, index VectorIndex, goal, device, taskType string) map[string]float64 {
	vectors, err := embedTexts(ctx, ai, []string{strings.Join([]string{goal, taskType, device}, "\n")})
	if err != nil || len(vectors) != 1 {
		return nil
	}
	if len(vectors[0]) != index.Dimension {
		return nil
	}
	out := map[string]float64{}
	for key, doc := range index.Documents {
		out[key] = cosineVector(vectors[0], doc.Vector)
	}
	return out
}

// embedTexts 调用 OpenAI 兼容的 /v1/embeddings 端点；模型与超时缺省值与
// Recall 共用同一套部署级默认，保证两个向量场景行为一致。
func embedTexts(ctx context.Context, ai AIConfig, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	endpoint := strings.TrimRight(strings.TrimSpace(ai.Endpoint), "/")
	if endpoint == "" {
		return nil, errors.New("embedding endpoint is empty")
	}
	if !strings.HasSuffix(endpoint, "/v1/embeddings") {
		endpoint += "/v1/embeddings"
	}
	model := strings.TrimSpace(ai.Model)
	if model == "" {
		model = recall.DefaultEmbeddingModel
	}
	payload, err := json.Marshal(map[string]any{"model": model, "input": texts})
	if err != nil {
		return nil, fmt.Errorf("encode embedding request: %w", err)
	}
	timeout := ai.Timeout
	if timeout <= 0 {
		timeout = recall.DefaultEmbeddingTimeout
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token := strings.TrimSpace(ai.APIKey); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxEmbeddingResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read embedding response: %w", err)
	}
	if len(data) > maxEmbeddingResponseBytes {
		return nil, fmt.Errorf("embedding response exceeds %d bytes", maxEmbeddingResponseBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embedding endpoint returned %s", resp.Status)
	}
	return parseEmbeddingResponse(data)
}

// parseEmbeddingResponse 同时兼容两种常见响应形态：
// {"embeddings": [[...]]} 与 OpenAI 的 {"data": [{"index": n, "embedding": [...]}]}；
// data 形态必须尊重 index 字段还原请求顺序，且不允许混用有无 index 的条目。
func parseEmbeddingResponse(data []byte) ([][]float64, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if value, exists := raw["embeddings"]; exists {
		values, ok := value.([]any)
		if !ok {
			return nil, errors.New("embedding response embeddings is not an array")
		}
		return vectorsFromArray(values)
	}
	if value, exists := raw["data"]; exists {
		dataValues, ok := value.([]any)
		if !ok {
			return nil, errors.New("embedding response data is not an array")
		}
		vectors := make([][]float64, len(dataValues))
		indexMode := -1
		for position, item := range dataValues {
			entry, ok := item.(map[string]any)
			if !ok {
				return nil, errors.New("embedding data item is not an object")
			}
			array, ok := entry["embedding"].([]any)
			if !ok {
				return nil, errors.New("embedding data item missing embedding")
			}
			vector, err := vectorFromArray(array)
			if err != nil {
				return nil, err
			}
			rawIndex, hasIndex := entry["index"]
			mode := 0
			if hasIndex {
				mode = 1
			}
			if indexMode == -1 {
				indexMode = mode
			} else if indexMode != mode {
				return nil, errors.New("embedding response mixes indexed and unindexed items")
			}
			target := position
			if hasIndex {
				index, ok := rawIndex.(float64)
				if !ok || index != math.Trunc(index) || index < 0 || int(index) >= len(dataValues) {
					return nil, fmt.Errorf("embedding response index is invalid: %v", rawIndex)
				}
				target = int(index)
			}
			if vectors[target] != nil {
				return nil, fmt.Errorf("embedding response index %d is duplicated", target)
			}
			vectors[target] = vector
		}
		for index, vector := range vectors {
			if vector == nil {
				return nil, fmt.Errorf("embedding response index %d is missing", index)
			}
		}
		return vectors, nil
	}
	return nil, errors.New("embedding response missing data or embeddings")
}

func vectorsFromArray(values []any) ([][]float64, error) {
	vectors := make([][]float64, 0, len(values))
	for _, item := range values {
		arr, ok := item.([]any)
		if !ok {
			return nil, errors.New("embedding item is not an array")
		}
		vector, err := vectorFromArray(arr)
		if err != nil {
			return nil, err
		}
		vectors = append(vectors, vector)
	}
	return vectors, nil
}

func vectorFromArray(values []any) ([]float64, error) {
	vector := make([]float64, 0, len(values))
	for _, value := range values {
		n, ok := value.(float64)
		if !ok {
			return nil, errors.New("embedding value is not a number")
		}
		vector = append(vector, n)
	}
	if len(vector) == 0 {
		return nil, errors.New("embedding vector is empty")
	}
	return vector, nil
}

// vectorText 拼接用于 embedding 的模板全文，顺序固定以保证同一模板文本稳定。
func vectorText(t Template) string {
	parts := []string{t.ID, t.Version, t.Title, t.Description, t.Match.Type}
	parts = append(parts, t.Match.Keywords...)
	parts = append(parts, t.Match.Devices...)
	parts = append(parts, t.CompletionConditions...)
	for _, step := range t.Steps {
		parts = append(parts, step.ID, step.Title, step.Phase)
	}
	if t.Contract != nil {
		parts = append(parts, t.Contract.Inputs...)
		parts = append(parts, t.Contract.Outputs...)
		parts = append(parts, t.Contract.Authority...)
		parts = append(parts, t.Contract.ForbiddenChanges...)
		parts = append(parts, t.Contract.Validation...)
		parts = append(parts, t.Contract.NextSkill)
	}
	return strings.Join(normalizeTexts(parts), "\n")
}

func cosineVector(a, b []float64) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
