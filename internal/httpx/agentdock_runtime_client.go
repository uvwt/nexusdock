package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type agentDockRuntimeClient struct {
	endpoint string
	token    string
	client   *http.Client
}

type agentDockRuntimeError struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e agentDockRuntimeError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Code != "" {
		return e.Code
	}
	return "AgentDock Runtime API unavailable"
}

func (s *Server) agentDockRuntimeClient() (*agentDockRuntimeClient, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(s.cfg.AgentDockEndpoint), "/")
	if endpoint == "" {
		return nil, agentDockRuntimeError{Code: "AGENTDOCK_ENDPOINT_UNCONFIGURED", Message: "AgentDock Runtime API 未配置"}
	}
	timeout := s.cfg.AgentDockTimeout
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	return &agentDockRuntimeClient{endpoint: endpoint, token: strings.TrimSpace(s.cfg.AgentDockToken), client: &http.Client{Timeout: timeout}}, nil
}

func (s *Server) runtimeGet(ctx context.Context, path string, query url.Values) (map[string]any, error) {
	return s.runtimeRequest(ctx, http.MethodGet, path, query, nil)
}

func (s *Server) runtimeDelete(ctx context.Context, path string) (map[string]any, error) {
	return s.runtimeRequest(ctx, http.MethodDelete, path, nil, nil)
}

func (s *Server) runtimePost(ctx context.Context, path string, payload any) (map[string]any, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode AgentDock Runtime request: %w", err)
	}
	return s.runtimeRequest(ctx, http.MethodPost, path, nil, body)
}

func (s *Server) runtimeRequest(ctx context.Context, method, path string, query url.Values, requestBody []byte) (map[string]any, error) {
	client, err := s.agentDockRuntimeClient()
	if err != nil {
		return nil, err
	}
	return client.request(ctx, method, path, query, requestBody)
}

func (c *agentDockRuntimeClient) request(ctx context.Context, method, path string, query url.Values, requestBody []byte) (map[string]any, error) {
	u := c.endpoint + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(requestBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if len(requestBody) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, agentDockRuntimeError{Code: "AGENTDOCK_RUNTIME_UNREACHABLE", Message: err.Error()}
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, agentDockRuntimeError{Status: resp.StatusCode, Code: "AGENTDOCK_RUNTIME_BAD_RESPONSE", Message: err.Error()}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, agentDockRuntimeError{Status: resp.StatusCode, Code: firstNonEmptyString(opsString(body["code"]), fmt.Sprintf("HTTP_%d", resp.StatusCode)), Message: firstNonEmptyString(opsString(body["error"]), resp.Status)}
	}
	return body, nil
}

func runtimeQueryLimitStatus(limit int, status string) url.Values {
	query := url.Values{}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	if strings.TrimSpace(status) != "" && status != "all" {
		query.Set("status", status)
	}
	return query
}

func runtimeUnavailablePayload(err error) map[string]any {
	code := "AGENTDOCK_RUNTIME_UNAVAILABLE"
	message := "AgentDock Runtime API 不可用"
	var rtErr agentDockRuntimeError
	if err != nil {
		if converted, ok := err.(agentDockRuntimeError); ok {
			rtErr = converted
		} else if converted, ok := err.(*agentDockRuntimeError); ok {
			rtErr = *converted
		}
		if rtErr.Code != "" {
			code = rtErr.Code
		}
		if err.Error() != "" {
			message = err.Error()
		}
	}
	return map[string]any{"ok": false, "available": false, "source": "agentdock-runtime-api", "code": code, "error": message}
}

func runtimeErrorHTTPStatus(err error) int {
	status := http.StatusServiceUnavailable
	switch converted := err.(type) {
	case agentDockRuntimeError:
		status = converted.Status
	case *agentDockRuntimeError:
		status = converted.Status
	}
	switch status {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusConflict:
		return status
	default:
		return http.StatusServiceUnavailable
	}
}
