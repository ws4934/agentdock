package task

import (
	"github.com/uvwt/agentdock/internal/config"
	toolcontract "github.com/uvwt/agentdock/internal/tool/contract"
)

const (
	ToolTaskRead   = "task_read"
	ToolTaskUpdate = "task_update"
)

// 同一个领域服务，按展示频率和副作用拆公开入口。UI 绑定属于工具定义，
// 不能靠运行后删除元数据来阻止客户端为高频检查点创建 iframe。
func feedbackInputSchema(name string, cfg config.Config) (map[string]any, bool) {
	if name == ToolTaskRead {
		props := map[string]any{
			"action":      map[string]any{"type": "string", "enum": []string{"list", "get", "snapshot"}, "description": "get reads the full task; snapshot reads a compact progress projection without logs or project scans."},
			"task_id":     toolcontract.String("Exact task id for get or snapshot."),
			"status":      map[string]any{"type": "string", "enum": []string{"active", "blocked", "completed"}},
			"limit":       toolcontract.BoundedInteger("List limit, default 50.", 1, 200),
			"if_revision": map[string]any{"type": "string", "pattern": "^tsk1:[a-f0-9]{64}$", "description": "snapshot-only: return unchanged=true without the projection when this revision still matches."},
		}
		schema := toolcontract.InputObject(props, "action")
		toolcontract.AddConstraint(schema, map[string]any{"if": map[string]any{"required": []string{"task_id"}}, "then": map[string]any{"properties": map[string]any{"action": map[string]any{"enum": []string{"get", "snapshot"}}}}})
		for _, action := range []string{"get", "snapshot"} {
			toolcontract.RequireWhen(schema, "action", action, "task_id")
		}
		for _, field := range []string{"status", "limit"} {
			toolcontract.AddConstraint(schema, map[string]any{"if": map[string]any{"required": []string{field}}, "then": map[string]any{"properties": map[string]any{"action": map[string]any{"const": "list"}}}})
		}
		toolcontract.AddConstraint(schema, map[string]any{"if": map[string]any{"required": []string{"if_revision"}}, "then": map[string]any{"properties": map[string]any{"action": map[string]any{"const": "snapshot"}}}})
		return schema, true
	}
	all := taskInputProperties(cfg)
	props := map[string]any{}
	var actions, fields []string
	switch name {
	case ToolTaskManage:
		actions = []string{"create", "block", "resume", "complete"}
		fields = []string{"task_id", "title", "goal", "completion_conditions", "project", "device", "steps", "summary", "template_id", "source_template_ids", "learning_checks"}
	case ToolTaskUpdate:
		actions = []string{"checkpoint", "final_review"}
		fields = []string{"task_id", "step_id", "completed_step_ids", "current_step_id", "status", "summary", "verified", "risks"}
	default:
		return nil, false
	}
	for _, field := range fields {
		if prop, ok := all[field]; ok {
			props[field] = prop
		}
	}
	props["action"] = map[string]any{"type": "string", "enum": actions}
	schema := toolcontract.InputObject(props, "action")
	for _, action := range actions {
		if action == "create" {
			toolcontract.RequireWhen(schema, "action", action, "title", "goal", "completion_conditions")
		} else {
			toolcontract.RequireWhen(schema, "action", action, "task_id")
		}
	}
	if name == ToolTaskUpdate {
		props["status"] = map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed", "pass", "failed"}}
		toolcontract.RequireWhen(schema, "action", "final_review", "status", "summary")
	}
	return schema, true
}

func feedbackOutputSchema(name string, cfg config.Config) (map[string]any, bool) {
	if name != ToolTaskManage && name != ToolTaskUpdate && name != ToolTaskRead {
		return nil, false
	}
	schema := ManageOutputSchema(cfg)
	props := schema["properties"].(map[string]any)
	switch name {
	case ToolTaskManage:
		props["action"] = map[string]any{"type": "string", "enum": []string{"create", "block", "resume", "complete"}}
	case ToolTaskUpdate:
		props["action"] = map[string]any{"type": "string", "enum": []string{"checkpoint", "final_review"}}
	case ToolTaskRead:
		props["action"] = map[string]any{"type": "string", "enum": []string{"list", "get", "snapshot"}}
	}
	if name == ToolTaskRead {
		props["revision"] = map[string]any{"type": "string", "pattern": "^tsk1:[a-f0-9]{64}$"}
		props["unchanged"] = toolcontract.Boolean("True only when the requested task revision matched. No task body is included.")
		props["partial"] = toolcontract.Boolean("The list is incomplete or includes unreadable entries.")
		toolcontract.RequireWhen(schema, "action", "snapshot", "task_id", "revision", "unchanged")
		toolcontract.AddConstraint(schema, map[string]any{"if": map[string]any{"properties": map[string]any{"unchanged": map[string]any{"const": true}}, "required": []string{"unchanged"}}, "then": map[string]any{"not": map[string]any{"anyOf": []any{map[string]any{"required": []string{"task"}}, map[string]any{"required": []string{"task_summary"}}}}}})
		toolcontract.AddConstraint(schema, map[string]any{"if": map[string]any{"properties": map[string]any{"action": map[string]any{"const": "snapshot"}, "unchanged": map[string]any{"const": false}}, "required": []string{"action", "unchanged"}}, "then": map[string]any{"required": []string{"task_summary"}}})
	}
	return schema, true
}
