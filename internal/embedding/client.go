package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	DefaultModel   = "BAAI/bge-m3"
	DefaultTimeout = 30 * time.Second

	maxResponseBytes   = 32 << 20
	maxStatusBodyBytes = 4096
)

// Client 只负责调用已经解析好的 Embedding HTTP endpoint。
// endpoint 路径策略属于调用域：Recall 与 Workflow 的历史规则不同，不能在这里统一。
type Client struct {
	httpClient *http.Client
}

type Request struct {
	URL    string
	Model  string
	APIKey string
	Inputs []string
}

// StatusError 保留非 2xx 响应的状态与受限响应体。
// 调用域可以按自己的历史错误语义格式化；Error() 与 Recall 现有错误文本保持一致。
type StatusError struct {
	Code   int
	Status string
	Body   string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("embedding endpoint returned HTTP %d: %s", e.Code, e.Body)
}

func NewClient(timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Client{httpClient: &http.Client{Timeout: timeout}}
}

func (c *Client) Embed(ctx context.Context, request Request) ([][]float64, error) {
	if len(request.Inputs) == 0 {
		return [][]float64{}, nil
	}
	endpoint := strings.TrimSpace(request.URL)
	if endpoint == "" {
		return nil, errors.New("embedding endpoint is empty")
	}
	payload, err := json.Marshal(struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}{
		Model: request.Model,
		Input: request.Inputs,
	})
	if err != nil {
		return nil, fmt.Errorf("encode embedding request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token := strings.TrimSpace(request.APIKey); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read embedding response: %w", err)
	}
	if len(data) > maxResponseBytes {
		return nil, fmt.Errorf("embedding response exceeds %d bytes", maxResponseBytes)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, &StatusError{
			Code:   response.StatusCode,
			Status: response.Status,
			Body:   truncateUTF8(strings.TrimSpace(string(data)), maxStatusBodyBytes),
		}
	}
	return ParseResponse(data)
}

// ParseResponse 只解析 Recall 与 Workflow 共同支持的两种批量响应：
// OpenAI data 形态与常见 embeddings 数组形态。Recall 的单 embedding 兼容留在 Recall 域。
func ParseResponse(data []byte) ([][]float64, error) {
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
		array, ok := item.([]any)
		if !ok {
			return nil, errors.New("embedding item is not an array")
		}
		vector, err := vectorFromArray(array)
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
		number, ok := value.(float64)
		if !ok {
			return nil, errors.New("embedding value is not a number")
		}
		vector = append(vector, number)
	}
	if len(vector) == 0 {
		return nil, errors.New("embedding vector is empty")
	}
	return vector, nil
}

func truncateUTF8(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for value != "" && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
