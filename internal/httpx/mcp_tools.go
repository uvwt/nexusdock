package httpx

import (
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	protocol "github.com/uvwt/agentdock-protocol"
	mcpcontract "github.com/uvwt/agentdock-protocol/mcpcontract"
)

type centralToolPresentation struct {
	name        string
	title       string
	description string
	uiURI       string
}

func nexusToolDefinitions() []*mcpsdk.Tool {
	return nexusToolDefinitionsWithApps(true)
}

func nexusToolDefinitionsWithApps(mcpAppsEnabled bool) []*mcpsdk.Tool {
	presentations := []centralToolPresentation{
		{name: mcpcontract.ToolAgentDockContext, title: "AgentDock fleet context", description: "Return one combined context for all enabled AgentDock nodes, including node-local capabilities and Nexus-owned shared Workflow and Recall context.", uiURI: protocol.ContextUIResourceURI},
		{name: mcpcontract.ToolRecallSearch, title: "Search NexusDock Recall", description: "Search Markdown documents and cards with lexical retrieval and optional semantic enhancement when embeddings are available."},
		{name: mcpcontract.ToolRecallRead, title: "Read NexusDock Recall entry", description: "Read one central Recall entry by path."},
		{name: mcpcontract.ToolRecallWrite, title: "Write NexusDock Recall entry", description: "Plan, create, replace, append, patch, update facts, diff, or delete central Recall content. The model must choose target and action explicitly.", uiURI: protocol.RecallUIResourceURI},
		{name: mcpcontract.ToolRecallMaintain, title: "Maintain NexusDock Recall", description: "Inspect sync/index state or rebuild the central Recall index."},
		{name: mcpcontract.ToolPrivateNoteManage, title: "Manage private notes", description: "Explicit low-frequency entrypoint for sensitive private notes."},
		{name: mcpcontract.ToolWorkflowTemplateManage, title: "Manage workflow templates", description: "List, get, get multiple, publish, retire, or match NexusDock workflow templates. get_many requires the model to compose the returned templates before task creation."},
	}
	tools := make([]*mcpsdk.Tool, 0, len(presentations))
	for _, presentation := range presentations {
		tools = append(tools, canonicalCentralToolWithApps(presentation, mcpAppsEnabled))
	}
	return tools
}

func canonicalCentralTool(presentation centralToolPresentation) *mcpsdk.Tool {
	return canonicalCentralToolWithApps(presentation, true)
}

func canonicalCentralToolWithApps(presentation centralToolPresentation, mcpAppsEnabled bool) *mcpsdk.Tool {
	input, ok := mcpcontract.InputSchema(presentation.name)
	if !ok {
		panic("missing canonical MCP input contract: " + presentation.name)
	}
	var output map[string]any
	if presentation.name == mcpcontract.ToolAgentDockContext {
		output = mcpcontract.FleetAgentDockContextOutputSchema()
	} else {
		var outputOK bool
		output, outputOK = mcpcontract.OutputSchema(presentation.name)
		if !outputOK {
			panic("missing canonical MCP output contract: " + presentation.name)
		}
	}
	annotations, ok := mcpcontract.AnnotationContract(presentation.name)
	if !ok {
		panic("missing canonical MCP annotations: " + presentation.name)
	}
	tool := &mcpsdk.Tool{
		Name:         presentation.name,
		Title:        presentation.title,
		Description:  presentation.description,
		InputSchema:  input,
		OutputSchema: output,
		Annotations:  canonicalCentralAnnotations(annotations),
	}
	if mcpAppsEnabled && presentation.uiURI != "" {
		tool.Meta = centralToolUIResourceMeta(presentation.uiURI)
	}
	return tool
}

func canonicalCentralAnnotations(value mcpcontract.Annotations) *mcpsdk.ToolAnnotations {
	annotations := &mcpsdk.ToolAnnotations{
		ReadOnlyHint:    value.ReadOnlyHint,
		DestructiveHint: value.DestructiveHint,
		OpenWorldHint:   value.OpenWorldHint,
	}
	if value.IdempotentHint != nil {
		annotations.IdempotentHint = *value.IdempotentHint
	}
	return annotations
}

func centralToolUIResourceMeta(uri string) mcpsdk.Meta {
	return mcpsdk.Meta{"ui": map[string]any{"resourceUri": uri}}
}
