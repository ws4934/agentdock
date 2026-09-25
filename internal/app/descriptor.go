package app

import "github.com/uvwt/agentdock/internal/mcpapps"

func ToolMetadata(def ToolDefinition, mcpAppsEnabled bool) map[string]any {
	meta := map[string]any{"agentdock/group": def.Group}
	if mcpAppsEnabled && def.UIBinding != nil {
		meta["ui"] = map[string]any{"resourceUri": mcpapps.ResourceURI(def.UIBinding.ResourceURI)}
	}
	if len(def.FileArgRewritePaths) > 0 {
		paths := append([]string(nil), def.FileArgRewritePaths...)
		meta["file_arg_rewrite_paths"] = paths
		meta["openai/fileParams"] = paths
	}
	if len(def.FileResultRewritePaths) > 0 {
		paths := append([]string(nil), def.FileResultRewritePaths...)
		meta["file_result_rewrite_paths"] = paths
		meta["openai/fileResultPaths"] = paths
		meta["openai/fileOutputs"] = paths
	}
	return meta
}

func MCPToolDescriptor(def ToolDefinition, mcpAppsEnabled bool) map[string]any {
	descriptor := map[string]any{
		"name":         def.Name,
		"title":        def.Title,
		"description":  def.Description,
		"inputSchema":  def.InputSchema,
		"outputSchema": def.OutputSchema,
	}
	if def.Annotations != nil {
		descriptor["annotations"] = map[string]any{
			"title": def.Annotations.Title, "readOnlyHint": def.Annotations.ReadOnlyHint,
			"destructiveHint": def.Annotations.DestructiveHint, "idempotentHint": def.Annotations.IdempotentHint,
			"openWorldHint": def.Annotations.OpenWorldHint,
		}
	}
	meta := ToolMetadata(def, mcpAppsEnabled)
	if paths, ok := meta["file_arg_rewrite_paths"].([]string); ok {
		descriptor["file_arg_rewrite_paths"] = paths
	}
	if paths, ok := meta["file_result_rewrite_paths"].([]string); ok {
		descriptor["file_result_rewrite_paths"] = paths
	}
	if len(meta) > 0 {
		descriptor["_meta"] = meta
	}

	return descriptor
}
