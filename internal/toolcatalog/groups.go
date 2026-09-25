// Package toolcatalog owns capability identities, not execution permissions.
package toolcatalog

import "fmt"

type Group struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

var definitions = []Group{
	{"core", "核心与诊断", "Bootstrap, tool discovery and safe runtime diagnostics."},
	{"files", "文件与检索", "Read, search and revision-guarded file editing."},
	{"execution", "命令与执行", "Command sessions, durable jobs and verified test execution."},
	{"project", "项目与语义", "Go semantic navigation, isolated worktrees and project evolution."},
	{"work", "任务与交付", "Task progress, evidence, immutable deliveries and explicit presentation."},
	{"integrations", "集成与 Skill", "Skill packages and independently configured dynamic MCP services."},
	{"media", "媒体与文件交付", "Image inspection and explicit immutable file publishing."},
	{"desktop", "桌面控制", "Locally approved desktop observation and input with snapshot guards."},
	{"browser", "浏览器", "Configured browser sessions, observation and actions."},
	{"acp", "编程代理", "Explicit external coding-agent sessions and interactions."},
	{"knowledge", "知识与工作流", "Configured Nexus memory, private notes and reusable workflows."},
}

func Groups() []Group { return append([]Group(nil), definitions...) }
func Lookup(id string) (Group, bool) {
	for _, g := range definitions {
		if g.ID == id {
			return g, true
		}
	}
	return Group{}, false
}
func Validate(ids []string) error {
	seen := map[string]bool{}
	for _, id := range ids {
		if _, ok := Lookup(id); !ok {
			return fmt.Errorf("unknown tool group %q", id)
		}
		if seen[id] {
			return fmt.Errorf("duplicate tool group %q", id)
		}
		seen[id] = true
	}
	return nil
}
func Enabled(id string, selection []string) bool {
	if len(selection) == 0 || id == "core" {
		return true
	}
	for _, value := range selection {
		if value == id {
			return true
		}
	}
	return false
}
