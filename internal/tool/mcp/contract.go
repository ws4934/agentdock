package mcp

import toolcontract "github.com/uvwt/agentdock/internal/tool/contract"

const (
	ToolManage  = "mcp_manage"
	ToolSearch  = "mcp_tool_search"
	ToolInspect = "mcp_tool_inspect"
	ToolCall    = "mcp_tool_call"
)

func InputSchema(name string) (map[string]any, bool) {
	stringProp := toolcontract.String
	boolProp := toolcontract.Boolean
	boundedIntProp := toolcontract.BoundedInteger
	props := map[string]any{}
	var required []string

	switch name {
	case ToolManage:
		props["action"] = map[string]any{"type": "string", "description": "Dynamic MCP server or isolated environment action.", "enum": []string{"list", "inspect", "add", "remove", "enable", "disable", "env_set", "env_unset", "env_list", "refresh"}}
		props["name"] = stringProp("Dynamic MCP server name. Use a stable short identifier such as figma or github.")
		props["description"] = stringProp("Short capability description shown in agentdock_context.")
		props["transport"] = map[string]any{"type": "string", "description": "MCP transport for action=add.", "enum": []string{"streamable_http", "stdio"}}
		props["url"] = stringProp("Absolute MCP endpoint URL for transport=streamable_http.")
		props["command"] = stringProp("Executable name or path for transport=stdio.")
		props["args"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Command arguments for transport=stdio."}
		props["cwd"] = stringProp("Optional absolute working directory for transport=stdio.")
		props["header_env"] = map[string]any{"type": "object", "description": "HTTP header name to host environment variable name. Secret values are never stored in the MCP registry.", "additionalProperties": map[string]any{"type": "string"}}
		props["env_from_env"] = map[string]any{"type": "object", "description": "Child process environment variable name to host environment variable name for stdio. Secret values are never stored in the MCP registry.", "additionalProperties": map[string]any{"type": "string"}}
		props["key"] = stringProp("Environment variable name for env_set/env_unset.")
		props["value"] = stringProp("Environment variable value for env_set. Secret values are never returned.")
		props["enabled"] = boolProp("Enable the server after registration. Defaults to true.")
		props["timeout_ms"] = boundedIntProp("Per-request timeout. Defaults to 30000 and is capped at 300000.", 1, 300000)
		required = []string{"action"}
	case ToolSearch:
		props["query"] = stringProp("Capability or tool query.")
		props["server"] = stringProp("Optional dynamic MCP server name from agentdock_context.")
		props["limit"] = boundedIntProp("Maximum matching tools. Defaults to 10 and is capped at 100.", 1, 100)
		required = []string{"query"}
	case ToolInspect:
		props["name"] = stringProp("Qualified dynamic MCP tool name in <server>:<tool> form.")
		required = []string{"name"}
	case ToolCall:
		props["name"] = stringProp("Qualified dynamic MCP tool name in <server>:<tool> form.")
		// arguments 的结构由上游 MCP tool schema 决定，是这里唯一需要保持开放的动态叶节点。
		props["arguments"] = map[string]any{"type": "object", "description": "Arguments matching the schema returned by mcp_tool_inspect.", "additionalProperties": true}
		required = []string{"name", "arguments"}
	default:
		return nil, false
	}
	schema := toolcontract.InputObject(props, required...)
	if name == ToolManage {
		for _, a := range []string{"inspect", "add", "remove", "enable", "disable", "env_set", "env_unset", "env_list", "refresh"} {
			toolcontract.RequireWhen(schema, "action", a, "name")
		}
		toolcontract.RequireWhen(schema, "action", "add", "transport")
		toolcontract.RequireWhen(schema, "transport", "stdio", "command")
		toolcontract.RequireWhen(schema, "transport", "streamable_http", "url")
		toolcontract.RequireWhen(schema, "action", "env_set", "key", "value")
		toolcontract.RequireWhen(schema, "action", "env_unset", "key")
	}
	return schema, true
}

func OutputSchema(name string) (map[string]any, bool) {
	stringProp := toolcontract.String
	intProp := toolcontract.Integer
	boolProp := toolcontract.Boolean
	arrayProp := toolcontract.ObjectArray
	objectProp := toolcontract.OpenObject
	props := map[string]any{}

	switch name {
	case ToolManage:
		props["action"] = stringProp("Completed dynamic MCP management action.")
		props["servers"] = arrayProp("Registered dynamic MCP server summaries.")
		props["server"] = objectProp("Dynamic MCP server summary.")
		props["config"] = objectProp("Dynamic MCP server configuration containing only non-secret values and environment variable names.")
		props["tools"] = arrayProp("Discovered lightweight MCP tool summaries.")
		props["tool_count"] = intProp("Discovered tool count.")
		props["count"] = intProp("Registered server count.")
		props["name"] = stringProp("Dynamic MCP server name.")
		props["removed"] = boolProp("Whether the server or environment variable was removed.")
		props["key"] = stringProp("Environment variable name. Secret values are never returned.")
		props["configured"] = boolProp("Whether the environment variable has a non-empty configured value.")
		props["items"] = arrayProp("Environment variable names and configured status without values.")
	case ToolSearch:
		props["query"] = stringProp("Capability query used.")
		props["server"] = stringProp("Optional server filter used.")
		props["tools"] = arrayProp("Matching lightweight MCP tool summaries.")
		props["count"] = intProp("Matching tool count.")
	case ToolInspect:
		props["name"] = stringProp("Qualified MCP tool name.")
		props["server"] = stringProp("Dynamic MCP server name.")
		props["tool_name"] = stringProp("Upstream MCP tool name.")
		props["title"] = stringProp("Tool title.")
		props["description"] = stringProp("Tool description.")
		props["input_schema"] = objectProp("Complete upstream MCP tool input schema.")
		props["output_schema"] = objectProp("Optional upstream MCP tool output schema.")
		props["annotations"] = objectProp("Optional upstream MCP tool annotations.")
	case ToolCall:
		props["name"] = stringProp("Qualified MCP tool name.")
		props["result"] = objectProp("Upstream tools/call metadata and structuredContent. On the MCP wire, large content appears once in the outer content field; result.content_location=mcp.content identifies this projection.")
	default:
		return nil, false
	}
	schema := toolcontract.OutputObject(props)
	switch name {
	case ToolManage:
		toolcontract.Require(schema, "action")
		toolcontract.RequireWhen(schema, "action", "list", "servers", "count")
		toolcontract.RequireWhen(schema, "action", "inspect", "config", "server")
		for _, a := range []string{"add", "enable", "disable", "refresh"} {
			toolcontract.RequireWhen(schema, "action", a, "server")
		}
		toolcontract.RequireWhen(schema, "action", "remove", "name", "removed")
		toolcontract.RequireWhen(schema, "action", "env_set", "name", "key", "configured")
		toolcontract.RequireWhen(schema, "action", "env_unset", "name", "key", "removed")
		toolcontract.RequireWhen(schema, "action", "env_list", "name", "items", "count")
	case ToolSearch:
		toolcontract.Require(schema, "query", "tools", "count")
	case ToolInspect:
		toolcontract.Require(schema, "name", "server", "tool_name", "input_schema")
	case ToolCall:
		toolcontract.Require(schema, "name", "result")
	}
	return schema, true
}
