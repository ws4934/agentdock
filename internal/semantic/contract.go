package semantic

import toolcontract "github.com/uvwt/agentdock/internal/tool/contract"

func InputSchema(name string) (map[string]any, bool) {
	if name != "code_navigate" {
		return nil, false
	}
	str := toolcontract.String
	integer := toolcontract.BoundedInteger
	return toolcontract.InputObject(map[string]any{"action": map[string]any{"type": "string", "enum": []string{"status", "document_symbols", "definition", "references", "hover", "diagnostics", "workspace_symbols"}}, "project": str("Exact native Go project root. Dependencies may be analyzed but results outside this root are omitted."), "path": str("Go source file within project."), "line": integer("1-based line for position requests.", 1, 10000000), "character": integer("Zero-based UTF-16 character offset. Must be a code-point boundary.", 0, 2000000), "query": str("Workspace symbol query, maximum 256 bytes."), "expected_read_revision": str("Optional exact source revision from read_file; mismatch fails."), "timeout_ms": integer("Bounded server lifetime, default 15000.", 100, 30000), "limit": integer("Maximum projected results, default 64.", 1, 128)}, "action"), true
}
func OutputSchema(name string) (map[string]any, bool) {
	if name != "code_navigate" {
		return nil, false
	}
	return toolcontract.OutputObject(map[string]any{"status": toolcontract.String("ready, stale, unknown, available, or not_installed."), "items": toolcontract.ObjectArray("Bounded workspace-only symbol/definition/reference/diagnostic locations."), "text": toolcontract.String("Plain-text hover content; never executable HTML."), "read_revision": toolcontract.String("Observed full-file content revision."), "diagnostics_complete": toolcontract.Boolean("Only true after an actual version-matched current diagnostics report."), "truncated": toolcontract.Boolean("Result bounds or workspace filtering omitted data."), "read_only": toolcontract.Boolean("No LSP mutation or command invocation is allowed.")}), true
}
