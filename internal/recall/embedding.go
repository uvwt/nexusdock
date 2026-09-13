package recall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	DefaultEmbeddingModel        = "BAAI/bge-m3"
	DefaultEmbeddingTimeout      = 30 * time.Second
	embeddingBatchSize           = 5
	embeddingBatchConcurrency    = 2
	embeddingTextMaxBytes        = 1536
	embeddingFrontmatterMaxBytes = 512
	embeddingHeadingsMaxBytes    = 640
	maxEmbeddingResponseBytes    = 32 << 20
)

type EmbeddingConfig struct {
	Enabled  bool
	Endpoint string
	Model    string
	APIKey   string
	Timeout  time.Duration
}

type EmbeddingService struct {
	store     *Store
	cfg       EmbeddingConfig
	indexPath string
	client    *http.Client
	mu        sync.Mutex
}

type EmbeddingReindexRequest struct {
	Prefix     string `json:"prefix"`
	MaxEntries int    `json:"max_entries"`
}

type EmbeddingReindexResult struct {
	OK        bool      `json:"ok"`
	Enabled   bool      `json:"enabled"`
	Model     string    `json:"model"`
	Endpoint  string    `json:"endpoint,omitempty"`
	IndexPath string    `json:"index_path"`
	Prefix    string    `json:"prefix"`
	Count     int       `json:"count"`
	Dimension int       `json:"dimension,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

type EmbeddingSearchRequest struct {
	Query         string `json:"query"`
	Prefix        string `json:"prefix"`
	ExcludePrefix string `json:"exclude_prefix,omitempty"`
	MaxResults    int    `json:"max_results"`
}

type EmbeddingSearchResult struct {
	OK      bool                    `json:"ok"`
	Enabled bool                    `json:"enabled"`
	Model   string                  `json:"model"`
	Query   string                  `json:"query"`
	Results []EmbeddingSearchHit    `json:"results"`
	Count   int                     `json:"count"`
	Index   EmbeddingIndexSummarize `json:"index"`
}

type EmbeddingSearchHit struct {
	Path        string            `json:"path"`
	Title       string            `json:"title,omitempty"`
	Score       float64           `json:"score"`
	Snippet     string            `json:"snippet"`
	Frontmatter map[string]string `json:"frontmatter,omitempty"`
}

type EmbeddingIndexSummarize struct {
	Model     string    `json:"model"`
	Dimension int       `json:"dimension,omitempty"`
	Count     int       `json:"count"`
	UpdatedAt time.Time `json:"updated_at"`
}

type embeddingIndex struct {
	Model     string                       `json:"model"`
	Dimension int                          `json:"dimension,omitempty"`
	UpdatedAt time.Time                    `json:"updated_at"`
	Documents map[string]embeddingDocument `json:"documents"`
}

type embeddingDocument struct {
	Path        string            `json:"path"`
	Title       string            `json:"title,omitempty"`
	Text        string            `json:"text"`
	Frontmatter map[string]string `json:"frontmatter,omitempty"`
	Vector      []float64         `json:"vector"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

func NewEmbeddingService(store *Store, cfg EmbeddingConfig) *EmbeddingService {
	if strings.TrimSpace(cfg.Model) == "" {
		cfg.Model = DefaultEmbeddingModel
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultEmbeddingTimeout
	}
	indexPath := ""
	if store != nil {
		indexPath = filepath.Join(store.Root(), ".recall", "embedding-index.json")
	}
	return &EmbeddingService{store: store, cfg: cfg, indexPath: indexPath, client: &http.Client{Timeout: cfg.Timeout}}
}

func (s *EmbeddingService) Enabled() bool {
	return s != nil && s.cfg.Enabled && strings.TrimSpace(s.cfg.Endpoint) != "" && s.store != nil
}

func (s *EmbeddingService) Status(ctx context.Context) map[string]any {
	status := map[string]any{
		"ok":         true,
		"enabled":    false,
		"model":      DefaultEmbeddingModel,
		"configured": false,
	}
	if s == nil {
		status["reason"] = "embedding service is not configured"
		return status
	}
	status["enabled"] = s.Enabled()
	status["configured"] = strings.TrimSpace(s.cfg.Endpoint) != ""
	status["model"] = s.cfg.Model
	status["endpoint"] = s.cfg.Endpoint
	status["index_path"] = s.indexPath
	idx, err := s.loadIndex()
	if err == nil {
		status["index"] = EmbeddingIndexSummarize{Model: idx.Model, Dimension: idx.Dimension, Count: len(idx.Documents), UpdatedAt: idx.UpdatedAt}
	}
	if s.Enabled() {
		ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
		defer cancel()
		if _, err := s.embed(ctx, []string{"health check"}); err != nil {
			status["reachable"] = false
			status["error"] = err.Error()
		} else {
			status["reachable"] = true
		}
	} else {
		status["reason"] = "enable vector search and configure an endpoint in NexusDock settings"
	}
	return status
}

func (s *EmbeddingService) Reindex(ctx context.Context, req EmbeddingReindexRequest) (EmbeddingReindexResult, error) {
	if !s.Enabled() {
		return EmbeddingReindexResult{}, errors.New("embedding service is disabled or endpoint is empty")
	}
	prefix := strings.Trim(filepath.ToSlash(strings.TrimSpace(req.Prefix)), "/")
	maxEntries := req.MaxEntries
	if maxEntries <= 0 || maxEntries > 2000 {
		maxEntries = 1000
	}
	entries, err := s.store.List(prefix, maxEntries)
	if err != nil {
		return EmbeddingReindexResult{}, err
	}
	docs := make([]embeddingDocument, 0, len(entries))
	for _, entry := range entries {
		if entry.Type != "file" || !IsTextFile(entry.Path) {
			continue
		}
		mem, err := s.store.Read(entry.Path)
		if err != nil {
			continue
		}
		text := embeddingText(mem)
		if text == "" {
			continue
		}
		docs = append(docs, embeddingDocument{Path: mem.Path, Title: firstMarkdownTitle(mem.Body), Text: text, Frontmatter: mem.Frontmatter, UpdatedAt: time.Now().UTC()})
	}

	// 重建不是“无条件重算”：内容和模型都没变的文档直接复用旧向量。
	// 这样日常维护只为新增/修改文档付推理成本，同时首次从 Cards-only 扩展到全 Recall 时仍会补齐缺失文档。
	vectors := make([][]float64, len(docs))
	existing, existingErr := s.loadIndex()
	canReuse := existingErr == nil && existing.Model == s.cfg.Model
	pending := make([]int, 0, len(docs))
	for i := range docs {
		if canReuse {
			if previous, ok := existing.Documents[docs[i].Path]; ok && previous.Text == docs[i].Text && len(previous.Vector) > 0 {
				vectors[i] = previous.Vector
				continue
			}
		}
		pending = append(pending, i)
	}

	// BGE-M3 对长文本批次的 CPU 推理较慢。保持每批很小，并只允许两批并行，
	// 既避免全量重建因纯串行推理拖得过长，也不把本机 embedding 服务打满。
	if err := s.embedPendingDocuments(ctx, docs, pending, vectors); err != nil {
		return EmbeddingReindexResult{}, err
	}
	if len(vectors) != len(docs) {
		return EmbeddingReindexResult{}, fmt.Errorf("embedding response count mismatch: got %d want %d", len(vectors), len(docs))
	}
	index := embeddingIndex{Model: s.cfg.Model, UpdatedAt: time.Now().UTC(), Documents: map[string]embeddingDocument{}}
	dimension := 0
	if prefix != "" && canReuse {
		// 局部重建只刷新目标前缀；其余同模型向量继续保留，避免 reindex_cards
		// 把已经建立好的全 Recall 索引重新缩成 Cards-only。
		dimension = existing.Dimension
		for path, document := range existing.Documents {
			if pathWithinPrefix(path, prefix) {
				continue
			}
			index.Documents[path] = document
		}
	}
	for i := range docs {
		if dimension == 0 {
			dimension = len(vectors[i])
		}
		if len(vectors[i]) != dimension {
			return EmbeddingReindexResult{}, fmt.Errorf("embedding dimension mismatch at result %d: got %d want %d", i, len(vectors[i]), dimension)
		}
		docs[i].Vector = vectors[i]
		index.Documents[docs[i].Path] = docs[i]
	}
	if len(index.Documents) == 0 {
		dimension = 0
	}
	index.Dimension = dimension
	if err := s.writeIndex(index); err != nil {
		return EmbeddingReindexResult{}, err
	}
	return EmbeddingReindexResult{OK: true, Enabled: true, Model: s.cfg.Model, Endpoint: s.cfg.Endpoint, IndexPath: s.indexPath, Prefix: prefix, Count: len(index.Documents), Dimension: dimension, UpdatedAt: index.UpdatedAt}, nil
}

func pathWithinPrefix(path, prefix string) bool {
	path = strings.Trim(filepath.ToSlash(strings.TrimSpace(path)), "/")
	prefix = strings.Trim(filepath.ToSlash(strings.TrimSpace(prefix)), "/")
	return prefix != "" && (path == prefix || strings.HasPrefix(path, prefix+"/"))
}

func (s *EmbeddingService) embedPendingDocuments(ctx context.Context, docs []embeddingDocument, pending []int, vectors [][]float64) error {
	if len(pending) == 0 {
		return nil
	}
	type batch struct {
		indices []int
		texts   []string
	}

	// Transformer batch 会 pad 到本批最长文本。按长度聚类后再切批，避免一篇长文
	// 迫使同批短文一起跑到相同序列长度，尤其适合 Recall 这种文档长度差异很大的语料。
	sort.SliceStable(pending, func(i, j int) bool {
		return len(docs[pending[i]].Text) < len(docs[pending[j]].Text)
	})

	workerCount := min(embeddingBatchConcurrency, (len(pending)+embeddingBatchSize-1)/embeddingBatchSize)
	jobs := make(chan batch)
	errCh := make(chan error, 1)
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var workers sync.WaitGroup
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range jobs {
				batchVectors, err := s.embed(workerCtx, job.texts)
				if err != nil {
					select {
					case errCh <- err:
					default:
					}
					cancel()
					return
				}
				if len(batchVectors) != len(job.indices) {
					select {
					case errCh <- fmt.Errorf("embedding response count mismatch: got %d want %d", len(batchVectors), len(job.indices)):
					default:
					}
					cancel()
					return
				}
				for i, documentIndex := range job.indices {
					vectors[documentIndex] = batchVectors[i]
				}
			}
		}()
	}

sendBatches:
	for start := 0; start < len(pending); start += embeddingBatchSize {
		end := min(start+embeddingBatchSize, len(pending))
		indices := append([]int(nil), pending[start:end]...)
		texts := make([]string, len(indices))
		for i, documentIndex := range indices {
			texts[i] = docs[documentIndex].Text
		}
		select {
		case jobs <- batch{indices: indices, texts: texts}:
		case <-workerCtx.Done():
			break sendBatches
		}
	}
	close(jobs)
	workers.Wait()

	select {
	case err := <-errCh:
		return err
	default:
		return workerCtx.Err()
	}
}

func (s *EmbeddingService) Search(ctx context.Context, req EmbeddingSearchRequest) (EmbeddingSearchResult, error) {
	if !s.Enabled() {
		return EmbeddingSearchResult{}, errors.New("embedding service is disabled or endpoint is empty")
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return EmbeddingSearchResult{}, errors.New("query is required")
	}
	maxResults := req.MaxResults
	if maxResults <= 0 || maxResults > 50 {
		maxResults = 8
	}
	idx, err := s.loadIndex()
	if err != nil {
		return EmbeddingSearchResult{}, err
	}
	if idx.Model != s.cfg.Model {
		return EmbeddingSearchResult{}, fmt.Errorf("embedding index model %q does not match configured model %q; rebuild the index", idx.Model, s.cfg.Model)
	}
	if len(idx.Documents) == 0 {
		return EmbeddingSearchResult{
			OK: true, Enabled: true, Model: s.cfg.Model, Query: query,
			Results: []EmbeddingSearchHit{}, Count: 0,
			Index: EmbeddingIndexSummarize{Model: idx.Model, Dimension: idx.Dimension, Count: 0, UpdatedAt: idx.UpdatedAt},
		}, nil
	}
	queryVectors, err := s.embed(ctx, []string{query})
	if err != nil {
		return EmbeddingSearchResult{}, err
	}
	if len(queryVectors) != 1 {
		return EmbeddingSearchResult{}, errors.New("embedding query returned no vector")
	}
	if len(queryVectors[0]) != idx.Dimension {
		return EmbeddingSearchResult{}, fmt.Errorf("embedding query dimension %d does not match index dimension %d; rebuild the index", len(queryVectors[0]), idx.Dimension)
	}
	prefix := strings.TrimSpace(req.Prefix)
	excludePrefix := strings.Trim(filepath.ToSlash(strings.TrimSpace(req.ExcludePrefix)), "/")
	hits := make([]EmbeddingSearchHit, 0, len(idx.Documents))
	for _, doc := range idx.Documents {
		if prefix != "" && !strings.HasPrefix(doc.Path, prefix) {
			continue
		}
		if excludePrefix != "" && (doc.Path == excludePrefix || strings.HasPrefix(doc.Path, excludePrefix+"/")) {
			continue
		}
		score := cosine(queryVectors[0], doc.Vector)
		hits = append(hits, EmbeddingSearchHit{Path: doc.Path, Title: doc.Title, Score: score, Snippet: snippetFromText(doc.Text), Frontmatter: doc.Frontmatter})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Path < hits[j].Path
	})

	// Recall 可以在两次 reindex 之间继续修改。旧向量不能作为 semantic-only 证据返回；
	// 文档变化时由 HybridSearch 的 lexical 基线兜底，下一次 reindex 后再恢复语义增强。
	freshHits := make([]EmbeddingSearchHit, 0, min(len(hits), maxResults))
	for _, hit := range hits {
		memory, err := s.store.Read(hit.Path)
		if err != nil || embeddingText(memory) != idx.Documents[hit.Path].Text {
			continue
		}
		freshHits = append(freshHits, hit)
		if len(freshHits) >= maxResults {
			break
		}
	}
	return EmbeddingSearchResult{OK: true, Enabled: true, Model: s.cfg.Model, Query: query, Results: freshHits, Count: len(freshHits), Index: EmbeddingIndexSummarize{Model: idx.Model, Dimension: idx.Dimension, Count: len(idx.Documents), UpdatedAt: idx.UpdatedAt}}, nil
}

// HybridSearch 以关键词检索为可靠基线；向量服务或索引不可用时直接退化为关键词结果。
// 两路都可用时按排名做 RRF 融合，避免依赖不同检索器不可直接比较的原始分数。
func (s *EmbeddingService) HybridSearch(ctx context.Context, options SearchOptions) ([]SearchResult, error) {
	maxResults := normalizeSearchResultLimit(options.MaxResults)
	lexicalOptions := options
	lexicalOptions.MaxResults = min(maxResults*2, 200)
	lexical, err := s.store.SearchWithOptions(lexicalOptions)
	if err != nil {
		return nil, err
	}
	if !s.Enabled() {
		return trimSearchResults(lexical, maxResults), nil
	}

	semantic, err := s.Search(ctx, EmbeddingSearchRequest{
		Query:         options.Query,
		Prefix:        options.Prefix,
		ExcludePrefix: options.ExcludePrefix,
		MaxResults:    min(max(maxResults*2, 8), 50),
	})
	if err != nil {
		return trimSearchResults(lexical, maxResults), nil
	}
	return fuseSearchResults(lexical, semantic.Results, maxResults), nil
}

func normalizeSearchResultLimit(maxResults int) int {
	if maxResults <= 0 || maxResults > 200 {
		return 50
	}
	return maxResults
}

func trimSearchResults(results []SearchResult, maxResults int) []SearchResult {
	if len(results) <= maxResults {
		return results
	}
	return results[:maxResults]
}

func fuseSearchResults(lexical []SearchResult, semantic []EmbeddingSearchHit, maxResults int) []SearchResult {
	const rrfK = 60.0
	type fusedResult struct {
		result SearchResult
		score  float64
	}

	byPath := make(map[string]*fusedResult, len(lexical)+len(semantic))
	for rank, result := range lexical {
		item := &fusedResult{result: result, score: 1 / (rrfK + float64(rank+1))}
		byPath[result.Path] = item
	}
	for rank, hit := range semantic {
		item := byPath[hit.Path]
		if item == nil {
			item = &fusedResult{result: SearchResult{
				Path:        hit.Path,
				Title:       hit.Title,
				Snippet:     hit.Snippet,
				Frontmatter: hit.Frontmatter,
			}}
			byPath[hit.Path] = item
		}
		item.score += 1 / (rrfK + float64(rank+1))
	}

	fused := make([]fusedResult, 0, len(byPath))
	for _, item := range byPath {
		fused = append(fused, *item)
	}
	sort.SliceStable(fused, func(i, j int) bool {
		if fused[i].score != fused[j].score {
			return fused[i].score > fused[j].score
		}
		return fused[i].result.Path < fused[j].result.Path
	})
	if len(fused) > maxResults {
		fused = fused[:maxResults]
	}
	results := make([]SearchResult, 0, len(fused))
	for _, item := range fused {
		results = append(results, item.result)
	}
	return results
}

func (s *EmbeddingService) embed(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return [][]float64{}, nil
	}
	endpoint := strings.TrimSpace(s.cfg.Endpoint)
	if endpoint == "" {
		return nil, errors.New("embedding endpoint is empty")
	}
	endpointURL, payload, err := embeddingRequest(endpoint, s.cfg.Model, texts)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode embedding request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token := strings.TrimSpace(s.cfg.APIKey); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, maxEmbeddingResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read embedding response: %w", err)
	}
	if len(data) > maxEmbeddingResponseBytes {
		return nil, fmt.Errorf("embedding response exceeds %d bytes", maxEmbeddingResponseBytes)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("embedding endpoint returned HTTP %d: %s", res.StatusCode, truncateUTF8(strings.TrimSpace(string(data)), 4096))
	}
	return parseEmbeddingResponse(data)
}

func embeddingRequest(endpoint, model string, texts []string) (string, map[string]any, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", nil, err
	}
	path := strings.TrimRight(u.Path, "/")
	if path == "" {
		u.Path = "/v1/embeddings"
		return u.String(), map[string]any{"model": model, "input": texts}, nil
	}
	if strings.HasSuffix(path, "/embed") {
		return u.String(), map[string]any{"model": model, "input": texts}, nil
	}
	return u.String(), map[string]any{"model": model, "input": texts}, nil
}

func parseEmbeddingResponse(data []byte) ([][]float64, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if dataValue, ok := raw["data"]; ok {
		items, ok := dataValue.([]any)
		if !ok {
			return nil, errors.New("embedding response data is not an array")
		}
		vectors := make([][]float64, len(items))
		indexMode := -1
		for position, item := range items {
			obj, ok := item.(map[string]any)
			if !ok {
				return nil, errors.New("embedding response item is not an object")
			}
			vector, err := vectorFromAny(obj["embedding"])
			if err != nil {
				return nil, err
			}
			rawIndex, hasIndex := obj["index"]
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
				if !ok || index != math.Trunc(index) || index < 0 || int(index) >= len(items) {
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
	if values, ok := raw["embeddings"]; ok {
		items, ok := values.([]any)
		if !ok {
			return nil, errors.New("embedding response embeddings is not an array")
		}
		vectors := make([][]float64, 0, len(items))
		for _, item := range items {
			vector, err := vectorFromAny(item)
			if err != nil {
				return nil, err
			}
			vectors = append(vectors, vector)
		}
		return vectors, nil
	}
	if value, ok := raw["embedding"]; ok {
		vector, err := vectorFromAny(value)
		if err != nil {
			return nil, err
		}
		return [][]float64{vector}, nil
	}
	return nil, errors.New("embedding response missing data, embeddings, or embedding")
}

func vectorFromAny(value any) ([]float64, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, errors.New("embedding vector is not an array")
	}
	vector := make([]float64, 0, len(items))
	for _, item := range items {
		number, ok := item.(float64)
		if !ok {
			return nil, errors.New("embedding vector contains a non-number")
		}
		vector = append(vector, number)
	}
	if len(vector) == 0 {
		return nil, errors.New("embedding vector is empty")
	}
	return vector, nil
}

func (s *EmbeddingService) loadIndex() (embeddingIndex, error) {
	data, err := os.ReadFile(s.indexPath)
	if err != nil {
		return embeddingIndex{}, err
	}
	var idx embeddingIndex
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&idx); err != nil {
		return embeddingIndex{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return embeddingIndex{}, errors.New("embedding index contains multiple JSON values")
		}
		return embeddingIndex{}, fmt.Errorf("read trailing embedding index data: %w", err)
	}
	if err := validateEmbeddingIndex(idx); err != nil {
		return embeddingIndex{}, err
	}
	return idx, nil
}

func validateEmbeddingIndex(idx embeddingIndex) error {
	if strings.TrimSpace(idx.Model) == "" {
		return errors.New("embedding index model is empty")
	}
	if idx.Documents == nil {
		return errors.New("embedding index documents are missing")
	}
	if len(idx.Documents) == 0 {
		if idx.Dimension != 0 {
			return errors.New("empty embedding index must have dimension 0")
		}
		return nil
	}
	if idx.Dimension <= 0 {
		return errors.New("embedding index dimension must be positive")
	}
	for path, document := range idx.Documents {
		if path == "" || document.Path != path {
			return fmt.Errorf("embedding index document path mismatch for %q", path)
		}
		if len(document.Vector) != idx.Dimension {
			return fmt.Errorf("embedding index document %q has dimension %d, want %d", path, len(document.Vector), idx.Dimension)
		}
	}
	return nil
}

func (s *EmbeddingService) writeIndex(idx embeddingIndex) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateEmbeddingIndex(idx); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.indexPath), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(s.indexPath, append(data, '\n'), 0o600)
}

func embeddingText(mem Recall) string {
	metadata := []string{
		mem.Path,
		firstMarkdownTitle(mem.Body),
		truncateUTF8(strings.TrimSpace(frontmatterText(mem.Frontmatter)), embeddingFrontmatterMaxBytes),
		markdownHeadingText(mem.Body),
	}
	prefix := strings.TrimSpace(strings.Join(metadata, "\n"))
	if len(prefix) >= embeddingTextMaxBytes {
		return strings.TrimSpace(truncateUTF8(prefix, embeddingTextMaxBytes))
	}
	remaining := embeddingTextMaxBytes - len(prefix)
	if prefix != "" {
		remaining--
	}
	body := semanticBodyExcerpt(mem.Body, remaining)
	return strings.TrimSpace(strings.Join([]string{prefix, body}, "\n"))
}

func markdownHeadingText(body string) string {
	headings := make([]string, 0, 8)
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		level := 0
		for level < len(trimmed) && level < 6 && trimmed[level] == '#' {
			level++
		}
		if level == 0 || level >= len(trimmed) || trimmed[level] != ' ' {
			continue
		}
		headings = append(headings, trimmed)
	}
	return strings.TrimSpace(truncateUTF8(strings.Join(headings, "\n"), embeddingHeadingsMaxBytes))
}

func semanticBodyExcerpt(body string, maxBytes int) string {
	body = strings.TrimSpace(body)
	if maxBytes <= 0 || body == "" {
		return ""
	}
	if len(body) <= maxBytes {
		return body
	}
	const separator = "\n...\n"
	if maxBytes <= len(separator)+2 {
		return strings.TrimSpace(truncateUTF8(body, maxBytes))
	}
	contentBudget := maxBytes - len(separator)
	headBudget := contentBudget * 3 / 4
	tailBudget := contentBudget - headBudget
	head := strings.TrimSpace(truncateUTF8(body, headBudget))
	tail := strings.TrimSpace(tailUTF8(body, tailBudget))
	return strings.TrimSpace(head + separator + tail)
}

func tailUTF8(value string, maxBytes int) string {
	if maxBytes <= 0 || value == "" {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	start := len(value) - maxBytes
	for start < len(value) && (value[start]&0xC0) == 0x80 {
		start++
	}
	return value[start:]
}

func snippetFromText(text string) string {
	text = strings.TrimSpace(text)
	if len(text) > 260 {
		return strings.TrimSpace(truncateUTF8(text, 260))
	}
	return text
}

func cosine(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
