package app

import "github.com/uvwt/agentdock/internal/tool/contract"

// ToolDiscoveryGuidance 同时用于启动上下文与 MCP initialize，避免两处引导漂移。
// 注册情况只是服务端事实；不能把它升级为客户端授权或本机操作权限。
const ToolDiscoveryGuidance = "不要因只发现读取工具就断言会话只读。执行/写入前核对 agentdock_context.tool_discovery；exec_command、file_edit 是内置工具。参数未展开时用 tool_catalog(format=\"mcp\", name=\"工具名\") 读取完整定义，再从客户端暴露的内置入口调用，不用 mcp_tool_call。Core 注册、客户端暴露/授权和本机权限必须分开判断；入口缺失就报告未暴露，调用失败就报告具体错误。明确禁用、拒绝或本机暂停/停止时不得换通道绕过；不要为探测权限而执行写操作。"

type toolDiscoveryContext struct {
	Source           string               `json:"source"`
	RegisteredCount  int                  `json:"registered_count"`
	ClientVisibility string               `json:"client_visibility"`
	ClientPermission string               `json:"client_permission"`
	LocalPermissions string               `json:"local_permissions"`
	KeyTools         []toolDiscoveryEntry `json:"key_tools"`
}

type toolDiscoveryEntry struct {
	Name       string `json:"name"`
	Group      string `json:"group"`
	Registered bool   `json:"registered"`
}

// 只读取已编译的运行时注册表，不重建全部 Schema、不联网，也不创建权限探针文件。
func (r *Runtime) toolDiscoveryContext() toolDiscoveryContext {
	result := toolDiscoveryContext{
		Source: "current_core_registry", RegisteredCount: len(r.toolNames),
		ClientVisibility: "not_observable", ClientPermission: "not_observable",
		LocalPermissions: "not_probed", KeyTools: []toolDiscoveryEntry{},
	}
	for _, name := range []string{"exec_command", "file_edit", "validation_run", "task_manage", "read_file", "tool_catalog"} {
		spec, exists := toolSpecByName(name)
		if !exists {
			continue
		}
		_, registered := r.toolValidators[name]
		result.KeyTools = append(result.KeyTools, toolDiscoveryEntry{Name: name, Group: spec.Group, Registered: registered})
	}
	return result
}

func toolDiscoverySchema() map[string]any {
	fixed := func(value, description string) map[string]any {
		return map[string]any{"type": "string", "const": value, "description": description}
	}
	entry := contract.InputObject(map[string]any{
		"name":       contract.String("Exact built-in tool name."),
		"group":      contract.String("Capability group, not a permission grant."),
		"registered": contract.Boolean("Registered by this Core. Does not prove client visibility or authorization."),
	}, "name", "group", "registered")
	return contract.InputObject(map[string]any{
		"source":            fixed("current_core_registry", "Observation source; not a client capability or permission probe."),
		"registered_count":  map[string]any{"type": "integer", "minimum": 0},
		"client_visibility": fixed("not_observable", "Core cannot inspect which tools the client exposed to this conversation."),
		"client_permission": fixed("not_observable", "Core cannot infer client approval from registration or prior calls."),
		"local_permissions": fixed("not_probed", "No filesystem write or command permission probe was performed."),
		"key_tools":         map[string]any{"type": "array", "maxItems": 6, "items": entry},
	}, "source", "registered_count", "client_visibility", "client_permission", "local_permissions", "key_tools")
}
