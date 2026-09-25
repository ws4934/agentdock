package app

import protocol "github.com/uvwt/agentdock-protocol"

// UIBinding describes when a tool should attach an MCP App resource to its descriptor or result.
// It is deliberately separate from the resource registry: binding a result does not prove that a node can serve the resource.
type UIBinding struct {
	ResourceURI string
}

// 中间执行/观察只返回数据；显式展示和文件交付才创建 iframe。
var toolUIBindings = map[string]UIBinding{
	"work_result_show":  {ResourceURI: "ui://agentdock/work-result"},
	"diagnostic_export": {ResourceURI: protocol.ArtifactUIResourceURI},
	"file_publish":      {ResourceURI: protocol.ArtifactUIResourceURI},
}

func toolUIBinding(name string) *UIBinding {
	binding, ok := toolUIBindings[name]
	if !ok {
		return nil
	}
	cloned := binding
	return &cloned
}
