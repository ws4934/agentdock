package desktop

import "github.com/uvwt/agentdock/internal/tool/contract"

const (
	ToolStatus      = "desktop_status"
	ToolPermissions = "desktop_permissions"
	ToolSnapshot    = "desktop_snapshot"
	ToolAct         = "desktop_act"
)

func InputSchema(name string) (map[string]any, bool) {
	props := map[string]any{}
	switch name {
	case ToolStatus:
		return contract.InputObject(props), true
	case ToolPermissions:
		props["permission"] = enum("macOS permission to request. This may display a system prompt; only call with user authorization.", "accessibility", "screen_recording")
		return contract.InputObject(props, "permission"), true
	case ToolSnapshot:
		props["display_id"] = contract.BoundedInteger("Display ID from a previous snapshot; default selects the main display.", 0, 4294967295)
		props["screenshot"] = contract.Boolean("Include an in-memory JPEG as MCP image content. Default true. Requires Screen Recording permission; no image is persisted or published.")
		props["accessibility"] = contract.Boolean("Include a bounded AX tree of the foreground app. Default false. Requires Accessibility permission; values and secure-field labels are not read.")
		props["max_dimension"] = contract.BoundedInteger("Maximum screenshot edge in pixels. Default 1568.", 128, 2048)
		props["max_nodes"] = contract.BoundedInteger("Maximum AX nodes. Default 200.", 1, 500)
		props["max_depth"] = contract.BoundedInteger("Maximum AX depth. Default 8.", 1, 16)
		return contract.InputObject(props), true
	case ToolAct:
		props["action"] = enum("One desktop action. Always observe again afterward; event dispatch is not proof that the application completed the task.", "activate", "click", "move", "drag", "scroll", "key", "type")
		props["snapshot_id"] = map[string]any{"type": "string", "pattern": "^[0-9a-f]{32}$", "description": "Fresh desktop_snapshot ID, expires after 30 seconds and is consumed by one attempted action."}
		props["pid"] = contract.BoundedInteger("Running application PID for activate, obtained from snapshot state.applications.", 1, 2147483647)
		props["point"] = pointSchema("Click/move position. Use exactly one of point or element_id for click.")
		props["space"] = enum("Coordinate space: screen is global logical points (default); image is pixels in this snapshot's screenshot. Never guess Retina scale.", "screen", "image")
		props["element_id"] = contract.String("AX element ID from this snapshot, for click via AXPress. Cannot combine with mouse options.")
		props["button"] = enum("Mouse button, default left.", "left", "right", "middle")
		props["click_count"] = contract.BoundedInteger("Coordinate click count, default 1.", 1, 3)
		props["path"] = map[string]any{"type": "array", "minItems": 2, "maxItems": 64, "items": pointSchema("Drag path point."), "description": "Drag path, including start and end. Mouse is always released before returning."}
		props["duration_ms"] = contract.BoundedInteger("Drag duration, default 500 ms.", 1, 2000)
		props["delta_x"] = contract.BoundedInteger("Horizontal scroll in pixels; positive sends the native positive axis.", -2000, 2000)
		props["delta_y"] = contract.BoundedInteger("Vertical scroll in pixels; positive scrolls up, negative scrolls down.", -2000, 2000)
		props["key"] = contract.String("Physical shortcut key: letters, digits, punctuation, enter/tab/escape/space/backspace/delete, arrows, home/end/pageup/pagedown, f1..f12. Use type for text.")
		props["modifiers"] = map[string]any{"type": "array", "maxItems": 4, "uniqueItems": true, "items": enum("Modifier.", "command", "cmd", "meta", "control", "ctrl", "option", "alt", "shift")}
		props["text"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 4096, "description": "Unicode text for type, sent directly via CGEvent. Does not read or overwrite the clipboard; Secure Input is refused."}
		schema := contract.InputObject(props, "action", "snapshot_id")
		schema["allOf"] = []map[string]any{
			when("activate", map[string]any{"required": []string{"pid"}}),
			when("move", map[string]any{"required": []string{"point"}}),
			when("drag", map[string]any{"required": []string{"path"}}),
			when("key", map[string]any{"required": []string{"key"}}),
			when("type", map[string]any{"required": []string{"text"}}),
			when("click", map[string]any{"oneOf": []map[string]any{{"type": "object", "required": []string{"point"}}, {"type": "object", "required": []string{"element_id"}}}}),
		}
		return schema, true
	default:
		return nil, false
	}
}
func enum(description string, values ...string) map[string]any {
	return map[string]any{"type": "string", "description": description, "enum": values}
}
func pointSchema(description string) map[string]any {
	s := contract.InputObject(map[string]any{"x": map[string]any{"type": "number"}, "y": map[string]any{"type": "number"}}, "x", "y")
	s["description"] = description
	return s
}
func when(action string, then map[string]any) map[string]any {
	then["type"] = "object"
	return map[string]any{"if": map[string]any{"type": "object", "properties": map[string]any{"action": map[string]any{"const": action}}, "required": []string{"action"}}, "then": then}
}
func OutputSchema(name string) (map[string]any, bool) {
	switch name {
	case ToolStatus, ToolPermissions:
		return contract.OutputObject(map[string]any{
			"enabled": contract.Boolean("Desktop capability is explicitly enabled."), "supported": contract.Boolean("Native backend is available."), "platform": contract.String("Host GOOS."),
			"permissions":    contract.OutputObject(map[string]any{"screen_recording": contract.Boolean("Screen Recording granted."), "accessibility": contract.Boolean("Accessibility granted."), "secure_input": contract.Boolean("Secure Input active.")}, "screen_recording", "accessibility", "secure_input"),
			"enable_setting": contract.String("Host environment setting."), "minimum_macos": contract.String("Minimum supported macOS version."), "permission_help": contract.String("User-facing authorization instructions."), "requested": contract.String("Permission requested, only in desktop_permissions."),
		}, "enabled", "supported", "platform", "permissions"), true
	case ToolSnapshot:
		return contract.OutputObject(map[string]any{
			"snapshot_id": contract.String("One-use snapshot identifier."), "captured_at": contract.String("Capture timestamp."), "expires_in_ms": contract.Integer("Snapshot validity."),
			"coordinate_system": contract.String("Coordinate convention."), "state": contract.OpenObject("Foreground PID, applications, displays with logical bounds, and onscreen windows."),
			"elements": contract.ObjectArray("AX element IDs, labels, roles, logical bounds, enabled/pressable state; no raw AX addresses or values."), "tree_truncated": contract.Boolean("AX traversal reached a node, depth, or time bound."), "image": contract.OpenObject("Image size, display ID, screen bounds and exact pixel-to-point scale."), "content_trust": contract.String("Screen-content trust boundary."),
		}, "snapshot_id", "captured_at", "expires_in_ms", "coordinate_system", "state", "elements", "tree_truncated"), true
	case ToolAct:
		return contract.OutputObject(map[string]any{
			"action": contract.String("Dispatched action."), "event_dispatched": contract.Boolean("System accepted the event for dispatch, not application-level confirmation."),
			"application_verified": contract.Boolean("False; another observation is required."), "next_required_action": contract.String("Observe again."), "snapshot_consumed": contract.Boolean("Snapshot cannot be reused."),
		}, "action", "event_dispatched", "application_verified", "next_required_action", "snapshot_consumed"), true
	default:
		return nil, false
	}
}
