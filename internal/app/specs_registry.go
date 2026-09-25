package app

import "github.com/uvwt/agentdock/internal/toolcatalog"

// buildToolSpecs 只负责公开工具顺序和能力分组。每个能力的描述、handler 与
// contract owner 放在对应 specs_<capability>.go，新增工具不再修改中央 Schema switch。
func buildToolSpecs() []ToolSpec {
	specs := make([]ToolSpec, 0, 32)
	specs = append(specs, grouped("core", contextToolSpecs())...)
	specs = append(specs, grouped("files", fileToolSpecs())...)
	specs = append(specs, grouped("execution", commandToolSpecs())...)
	specs = append(specs, grouped("work", taskManageToolSpecs())...)
	specs = append(specs, grouped("work", workToolSpecs())...)
	specs = append(specs, grouped("project", projectToolSpecs())...)
	specs = append(specs, grouped("core", diagnosticsToolSpecs())...)
	specs = append(specs, grouped("project", evolutionToolSpecs())...)
	specs = append(specs, grouped("acp", acpToolSpecs())...)
	specs = append(specs, grouped("knowledge", workflowToolSpecs())...)
	specs = append(specs, grouped("integrations", skillToolSpecs())...)
	specs = append(specs, grouped("integrations", dynamicMCPToolSpecs())...)
	specs = append(specs, grouped("desktop", desktopToolSpecs())...)
	specs = append(specs, grouped("media", imageToolSpecs())...)
	specs = append(specs, grouped("knowledge", recallToolSpecs())...)
	specs = append(specs, grouped("browser", browserToolSpecs())...)
	specs = append(specs, grouped("media", publishToolSpecs())...)
	specs = append(specs, grouped("core", catalogToolSpecs())...)
	return specs
}

var toolSpecs []ToolSpec
var toolSpecIndex map[string]ToolSpec

// 目录工具引用整个注册表；在 init 中一次构造，避免声明初始化的自引用循环。
func init() {
	toolSpecs = buildToolSpecs()
	toolSpecIndex = indexToolSpecs(toolSpecs)
}

func indexToolSpecs(specs []ToolSpec) map[string]ToolSpec {
	index := make(map[string]ToolSpec, len(specs))
	for _, spec := range specs {
		if spec.Name == "" {
			panic("tool spec name must not be empty")
		}
		if spec.Contract == nil {
			panic("tool spec contract must not be nil: " + spec.Name)
		}
		if spec.Handler == nil {
			panic("tool spec handler must not be nil: " + spec.Name)
		}
		if _, exists := index[spec.Name]; exists {
			panic("duplicate tool spec: " + spec.Name)
		}
		index[spec.Name] = spec
	}
	return index
}

func grouped(group string, specs []ToolSpec) []ToolSpec {
	if _, ok := toolcatalog.Lookup(group); !ok {
		panic("unknown tool group: " + group)
	}
	for i := range specs {
		specs[i].Group = group
	}
	return specs
}
