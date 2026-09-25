package app

import protocol "github.com/uvwt/agentdock-protocol"

// UIBinding describes when a tool should attach an MCP App resource to its descriptor or result.
// It is deliberately separate from the resource registry: binding a result does not prove that a node can serve the resource.
type UIBinding struct {
	ResourceURI string
}

// 已有操作反馈默认展示，包括文件变更；不能为减少 iframe 一刀切关闭操作卡片。
// 纯观察及高频 task_update/task_read 仍不绑定资源。
// 描述符与结果共享此表，不能仅删结果元数据来抑制宿主挂载。
var toolUIBindings = map[string]UIBinding{
	"agentdock_context":        {ResourceURI: protocol.ContextUIResourceURI},
	"file_edit":                {ResourceURI: protocol.FileChangeUIResourceURI},
	"mcp_tool_call":            {ResourceURI: protocol.DynamicMCPUIResourceURI},
	"acp_session":              {ResourceURI: protocol.ACPStatusUIResourceURI},
	"workflow_template_manage": {ResourceURI: protocol.WorkflowUIResourceURI},
	"recall_write":             {ResourceURI: protocol.RecallUIResourceURI},
	"task_manage":              {ResourceURI: protocol.TaskProgressUIResourceURI},
	"work_result_freeze":       {ResourceURI: "ui://agentdock/work-result"},
	"work_result_show":         {ResourceURI: "ui://agentdock/work-result"},
	"diagnostic_export":        {ResourceURI: protocol.ArtifactUIResourceURI},
	"file_publish":             {ResourceURI: protocol.ArtifactUIResourceURI},
}

func toolUIBinding(name string) *UIBinding {
	binding, ok := toolUIBindings[name]
	if !ok {
		return nil
	}
	cloned := binding
	return &cloned
}
