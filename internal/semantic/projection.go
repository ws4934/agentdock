package semantic

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
)

type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}
type span struct {
	Start position `json:"start"`
	End   position `json:"end"`
}

func publicRange(raw json.RawMessage) (map[string]any, bool) {
	var required struct {
		Start *struct {
			Line      *int `json:"line"`
			Character *int `json:"character"`
		} `json:"start"`
		End *struct {
			Line      *int `json:"line"`
			Character *int `json:"character"`
		} `json:"end"`
	}
	if json.Unmarshal(raw, &required) != nil || required.Start == nil || required.End == nil || required.Start.Line == nil || required.Start.Character == nil || required.End.Line == nil || required.End.Character == nil {
		return nil, false
	}
	var r span
	if len(raw) == 0 || json.Unmarshal(raw, &r) != nil || r.Start.Line < 0 || r.End.Line < r.Start.Line || r.Start.Character < 0 || r.End.Character < 0 || r.End.Line > 10000000 {
		return nil, false
	}
	if r.End.Line == r.Start.Line && r.End.Character < r.Start.Character {
		return nil, false
	}
	return map[string]any{"start_line": r.Start.Line + 1, "start_character_utf16": r.Start.Character, "end_line": r.End.Line + 1, "end_character_utf16": r.End.Character}, true
}
func clipped(s string, n int) string {
	if len(s) > n {
		return strings.ToValidUTF8(s[:n], "�")
	}
	return s
}
func projectResult(root, path, action string, raw json.RawMessage, limit int) (map[string]any, error) {
	result := map[string]any{"action": action, "scope": "workspace_only", "items": []map[string]any{}}
	if action == "hover" {
		var value struct {
			Contents json.RawMessage `json:"contents"`
			Range    json.RawMessage `json:"range"`
		}
		if json.Unmarshal(raw, &value) != nil {
			return nil, errors.New("invalid hover result")
		}
		var collect func(json.RawMessage, int) string
		collect = func(data json.RawMessage, depth int) string {
			if depth > 2 {
				return ""
			}
			var s string
			if json.Unmarshal(data, &s) == nil {
				return clipped(s, 8192)
			}
			var markup struct {
				Value string `json:"value"`
			}
			if json.Unmarshal(data, &markup) == nil && markup.Value != "" {
				return clipped(markup.Value, 8192)
			}
			var values []json.RawMessage
			if json.Unmarshal(data, &values) != nil {
				return ""
			}
			parts := []string{}
			for i, v := range values {
				if i >= 16 {
					break
				}
				parts = append(parts, collect(v, depth+1))
			}
			return clipped(strings.Join(parts, "\n"), 8192)
		}
		text := collect(value.Contents, 0)
		result["text"] = text
		result["truncated"] = len(text) >= 8192
		return result, nil
	}
	if action == "diagnostics" {
		if len(raw) == 0 || string(raw) == "null" {
			return nil, errors.New("language server omitted diagnostic report")
		}
		if len(raw) > 0 && raw[0] == '{' {
			var value struct {
				Kind  string            `json:"kind"`
				Items []json.RawMessage `json:"items"`
			}
			if json.Unmarshal(raw, &value) != nil || (value.Kind != "full" && value.Kind != "") || value.Items == nil {
				return nil, errors.New("diagnostics are not a complete current report")
			}
			if value.Kind != "full" {
				result["diagnostics_report_confirmed"] = false
			}
			raw, _ = json.Marshal(value.Items)
		}
	}
	var values []json.RawMessage
	if string(raw) == "null" || len(raw) == 0 {
		return result, nil
	}
	if raw[0] == '{' {
		values = []json.RawMessage{raw}
	} else if json.Unmarshal(raw, &values) != nil {
		return nil, errors.New("invalid language server result")
	}
	items := []map[string]any{}
	partial := false
	outside := 0
	processed := 0
	var appendValue func(json.RawMessage, string, int)
	appendValue = func(raw json.RawMessage, container string, depth int) {
		processed++
		if len(items) >= limit || processed > 2048 || depth > 16 {
			partial = true
			return
		}
		var value struct {
			Name                 string            `json:"name"`
			Kind                 int               `json:"kind"`
			URI                  string            `json:"uri"`
			TargetURI            string            `json:"targetUri"`
			Range                json.RawMessage   `json:"range"`
			SelectionRange       json.RawMessage   `json:"selectionRange"`
			TargetSelectionRange json.RawMessage   `json:"targetSelectionRange"`
			Message              string            `json:"message"`
			Severity             int               `json:"severity"`
			Children             []json.RawMessage `json:"children"`
			Location             struct {
				URI   string          `json:"uri"`
				Range json.RawMessage `json:"range"`
			} `json:"location"`
		}
		if json.Unmarshal(raw, &value) != nil {
			partial = true
			return
		}
		uri := value.URI
		rangeData := value.Range
		if value.Location.URI != "" {
			uri = value.Location.URI
			rangeData = value.Location.Range
		}
		if value.TargetURI != "" {
			uri = value.TargetURI
			rangeData = value.TargetSelectionRange
		}
		if len(value.SelectionRange) > 0 {
			rangeData = value.SelectionRange
		}
		relative := ""
		if uri != "" {
			var ok bool
			relative, ok = uriPath(root, uri)
			if !ok {
				outside++
				partial = true
				return
			}
		} else {
			relative, _ = filepath.Rel(root, path)
			relative = filepath.ToSlash(relative)
		}
		r, ok := publicRange(rangeData)
		if !ok {
			partial = true
			return
		}
		row := map[string]any{"path": relative, "range": r}
		if value.Name != "" {
			row["name"] = clipped(value.Name, 512)
			row["kind"] = value.Kind
			if container != "" {
				row["container"] = clipped(container, 512)
			}
		}
		if action == "diagnostics" {
			row["message"] = clipped(value.Message, 2048)
			row["severity"] = value.Severity
		}
		items = append(items, row)
		for _, child := range value.Children {
			appendValue(child, value.Name, depth+1)
		}
	}
	for _, v := range values {
		appendValue(v, "", 0)
	}
	result["items"] = items
	result["truncated"] = partial
	result["outside_workspace_omitted"] = outside
	return result, nil
}
