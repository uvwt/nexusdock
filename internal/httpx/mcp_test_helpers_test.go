package httpx

import (
	"context"
	"encoding/json"
	"testing"
)

func mustToolJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func callNexusToolForTest(t *testing.T, server *Server, ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	t.Helper()
	return server.callNexusTool(ctx, name, mustToolJSON(t, args))
}

func callRecallWriteForTest(t *testing.T, server *Server, ctx context.Context, args map[string]any) (map[string]any, error) {
	t.Helper()
	input, err := decodeRecallWriteInput(mustToolJSON(t, args))
	if err != nil {
		return nil, err
	}
	return server.callRecallWrite(ctx, input)
}

func callRecallMaintainForTest(t *testing.T, server *Server, ctx context.Context, args map[string]any) (map[string]any, error) {
	t.Helper()
	var input recallMaintainInput
	if err := decodeToolInput(mustToolJSON(t, args), &input); err != nil {
		return nil, err
	}
	return server.callRecallMaintain(ctx, input)
}

func callWorkflowTemplateManageForTest(t *testing.T, server *Server, ctx context.Context, args map[string]any) (map[string]any, error) {
	t.Helper()
	var input workflowTemplateManageInput
	if err := decodeToolInput(mustToolJSON(t, args), &input); err != nil {
		return nil, err
	}
	return server.callWorkflowTemplateManage(ctx, input)
}

func updateRecallFactsForTest(t *testing.T, server *Server, ctx context.Context, path string, args map[string]any) (map[string]any, error) {
	t.Helper()
	args = cloneTestArgs(args)
	args["path"] = path
	input, err := decodeRecallWriteInput(mustToolJSON(t, args))
	if err != nil {
		return nil, err
	}
	return server.updateRecallFacts(ctx, input)
}

func centralToolResultMetaForTest(t *testing.T, name string, args map[string]any) map[string]any {
	t.Helper()
	return centralToolResultMeta(name, mustToolJSON(t, args))
}

func centralToolResultMetaWithAppsForTest(t *testing.T, name string, args map[string]any, enabled bool) map[string]any {
	t.Helper()
	return centralToolResultMetaWithApps(name, mustToolJSON(t, args), enabled)
}

func cloneTestArgs(args map[string]any) map[string]any {
	cloned := make(map[string]any, len(args)+1)
	for key, value := range args {
		cloned[key] = value
	}
	return cloned
}
