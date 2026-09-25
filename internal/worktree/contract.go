package worktree

import toolcontract "github.com/uvwt/agentdock/internal/tool/contract"

func InputSchema(name string) (map[string]any, bool) {
	if name != "worktree_manage" {
		return nil, false
	}
	str := toolcontract.String
	return toolcontract.InputObject(map[string]any{"action": map[string]any{"type": "string", "enum": []string{"create", "status", "list"}}, "request_id": str("Stable idempotency key for creation; no automatic retry of incomplete worktrees."), "worktree_id": str("Managed worktree id for status."), "project": str("Exact source Git working root."), "base_commit": str("Exact local 40-hex commit, not a branch/tag/revision expression."), "label": str("Short display label.")}, "action"), true
}
func OutputSchema(name string) (map[string]any, bool) {
	if name != "worktree_manage" {
		return nil, false
	}
	return toolcontract.OutputObject(map[string]any{"worktree": toolcontract.OpenObject("Managed edit-isolation workspace; not a process sandbox."), "worktrees": toolcontract.ObjectArray("Managed workspace registrations."), "truncated": toolcontract.Boolean("More workspaces exist than the listing bound.")}), true
}
