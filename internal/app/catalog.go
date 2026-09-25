package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/uvwt/agentdock/internal/config"
	contract "github.com/uvwt/agentdock/internal/tool/contract"
	"github.com/uvwt/agentdock/internal/toolcatalog"
)

type CatalogRequest struct {
	Format string `json:"format,omitempty"`
	Group  string `json:"group,omitempty"`
}

func catalogContract(name string, _ config.Config) (ToolContract, bool) {
	if name != "tool_catalog" {
		return ToolContract{}, false
	}
	return ToolContract{
		InputSchema:  contract.InputObject(map[string]any{"format": map[string]any{"type": "string", "enum": []string{"summary", "mcp", "openai"}, "description": "summary is compact; mcp/openai explicitly export schemas without changing enabled tools."}, "group": contract.String("Optional exact capability group id.")}),
		OutputSchema: contract.OutputObject(map[string]any{"groups": contract.ObjectArray("Available groups."), "tools": contract.ObjectArray("Tools or namespace definitions, according to format."), "format": contract.String("Selected output format."), "count": contract.Integer("Available function count."), "catalog_revision": contract.String("Hash of this exact catalog."), "observation_only": contract.Boolean("No registration or permissions changed.")}, "groups", "tools", "format", "count", "catalog_revision", "observation_only"),
	}, true
}
func catalogToolSpecs() []ToolSpec {
	return []ToolSpec{{Name: "tool_catalog", Title: "Inspect grouped tools", Description: "Read the available tool catalog by capability group. Default returns compact names; explicitly select mcp or openai to export schemas. Does not enable tools, change permissions or make a client support lazy loading.", Contract: catalogContract, Annotations: readOnlyToolAnnotations(false), Handler: typedToolHandler("tool_catalog", func(_ context.Context, r *Runtime, request CatalogRequest) (Result, error) {
		return ExportToolCatalog(r.cfg, request)
	})}}
}

// ExportToolCatalog is pure discovery: no Runtime, child processes, config writes or network.
func ExportToolCatalog(cfg config.Config, r CatalogRequest) (Result, error) {
	if err := toolcatalog.Validate(cfg.ToolGroups); err != nil {
		return nil, err
	}
	if r.Format == "" {
		r.Format = "summary"
	}
	if r.Format != "summary" && r.Format != "mcp" && r.Format != "openai" {
		return nil, fmt.Errorf("unsupported catalog format")
	}
	if r.Group != "" {
		if _, ok := toolcatalog.Lookup(r.Group); !ok {
			return nil, fmt.Errorf("unknown group %q", r.Group)
		}
	}
	groups := []map[string]any{}
	tools := []map[string]any{}
	count := 0
	for _, group := range toolcatalog.Groups() {
		if r.Group != "" && r.Group != group.ID {
			continue
		}
		members := []map[string]any{}
		for _, spec := range toolSpecs {
			if spec.Group != group.ID || !spec.available(cfg) {
				continue
			}
			d := spec.definition(cfg)
			count++
			var item map[string]any
			switch r.Format {
			case "summary":
				item = map[string]any{"name": d.Name, "title": d.Title, "group": d.Group, "read_only": d.Annotations != nil && d.Annotations.ReadOnlyHint, "presentation": cfg.MCPAppsEnabled && d.UIBinding != nil}
			case "mcp":
				item = MCPToolDescriptor(d, cfg.MCPAppsEnabled)
			case "openai":
				item = map[string]any{"type": "function", "name": d.Name, "description": d.Description, "parameters": d.InputSchema, "strict": false, "defer_loading": d.Group != "core"}
			}
			members = append(members, item)
		}
		if len(members) == 0 {
			continue
		}
		groups = append(groups, map[string]any{"id": group.ID, "title": group.Title, "description": group.Description, "count": len(members)})
		if r.Format == "openai" {
			tools = append(tools, map[string]any{"type": "namespace", "name": group.ID, "description": group.Description, "tools": members})
		} else {
			tools = append(tools, members...)
		}
	}
	if r.Format == "openai" {
		tools = append(tools, map[string]any{"type": "tool_search"})
	}
	encoded, _ := json.Marshal([]any{groups, tools})
	return Result{"groups": groups, "tools": tools, "format": r.Format, "count": count, "catalog_revision": fmt.Sprintf("sha256:%x", sha256.Sum256(encoded)), "observation_only": true}, nil
}

// 接入层收到 namespace/name 后仍走 Runtime.Call 的相同可用性、参数和执行校验。
func (r *Runtime) CallNamespaced(ctx context.Context, namespace, name string, args map[string]any) (Result, error) {
	d, ok := r.ToolDefinition(name)
	if !ok || d.Group != namespace {
		return nil, toolError("UNKNOWN_TOOL", "tool is not available in this namespace", "validation")
	}
	return r.Call(ctx, name, args)
}
