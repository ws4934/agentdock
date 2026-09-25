package app

import (
	"github.com/uvwt/agentdock/internal/tool/contract"
)

// 本地适配共享协议的输出约束，不修改 Go module cache 或放宽任意字段。
func localContextSchema(schema map[string]any) map[string]any {
	props := schema["properties"].(map[string]any)
	props["tool_discovery"] = toolDiscoverySchema()
	contract.Require(schema, "tool_discovery")
	props["acp"] = map[string]any{"type": "object", "additionalProperties": false,
		"required": []string{"enabled", "default_profile", "profiles", "description"},
		"properties": map[string]any{
			"enabled":         contract.Boolean("Whether ACP is enabled."),
			"default_profile": contract.String("Configured default profile id."),
			"profiles":        map[string]any{"type": "array", "items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"id", "kind"}, "properties": map[string]any{"id": contract.String("Profile id."), "kind": contract.String("Coding agent kind.")}}},
			"description":     contract.String("Short ACP orientation."),
		}}
	return schema
}
func constrainSharedOutput(name string, schema map[string]any) {
	switch name {
	case "recall_search":
		contract.Require(schema, "results", "count", "query")
	case "recall_read":
		contract.Require(schema, "recall")
	case "recall_write":
		contract.Require(schema, "recall_action", "recall_target")
	case "recall_maintain":
		contract.Require(schema, "recall_action")
		contract.RequireWhen(schema, "recall_action", "list", "entries", "count")
		contract.RequireWhen(schema, "recall_action", "lint", "findings", "finding_count")
	case "private_note_manage":
		contract.Require(schema, "action")
		contract.RequireWhen(schema, "action", "read", "path", "content")
		contract.RequireWhen(schema, "action", "search", "results", "metadata_only")
	case "workflow_template_manage":
		contract.Require(schema, "action")
		contract.RequireWhen(schema, "action", "list", "templates", "count")
		contract.RequireWhen(schema, "action", "get", "template")
		contract.RequireWhen(schema, "action", "get_many", "templates", "composition_required")
		contract.RequireWhen(schema, "action", "match", "candidates")
		for _, a := range []string{"publish", "retire"} {
			contract.RequireWhen(schema, "action", a, "template_id")
		}
	}
}
