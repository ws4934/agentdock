package mcp

import (
	"encoding/json"
	"fmt"
	"github.com/uvwt/agentdock/internal/publicartifacts"
)

// 最终预算包括结构化数据、文本和图像；超限保留完整私有结果，不伪造截断成功。
func (s *Server) boundedEnvelope(name string, result map[string]any) map[string]any {
	if s.cfg.ResultTextMode == "json" && name != "mcp_tool_call" {
		payload, err := json.Marshal(result["structuredContent"])
		if err == nil {
			blocks := []map[string]any{{"type": "text", "text": string(payload)}}
			if original, ok := result["content"].([]map[string]any); ok {
				for _, block := range original {
					if block["type"] != "text" {
						blocks = append(blocks, block)
					}
				}
			}
			result["content"] = blocks
		}
	}
	limit := s.cfg.MaxToolResultBytes
	if limit == 0 {
		limit = 16 << 20
	}
	data, err := json.Marshal(result)
	if err == nil && len(data) <= limit {
		return result
	}
	details := map[string]any{"tool": name, "code": "RESULT_DELIVERY_FAILED", "retryable": false, "do_not_replay": true, "output_complete": false, "original_is_error": result["isError"] == true}
	message := "Tool result delivery failed; effects may already have occurred. Do not replay mutations."
	if err == nil && len(data) <= 64<<20 && s.cfg.AgentDockHome != "" {
		store := publicartifacts.New(s.cfg.AgentDockHome, "", 0)
		saved, saveErr := store.PublishBytes(publicartifacts.PublishBytesRequest{Private: true, Filename: "tool-result.json", MimeType: "application/json", Data: data, RetentionSeconds: 3600})
		if saveErr == nil {
			details["code"] = "RESULT_DEFERRED"
			details["full_result"] = map[string]any{"artifact_id": saved.ArtifactID, "resource_uri": publicartifacts.ResourceURI(saved.ArtifactID), "sha256": saved.SHA256, "size_bytes": saved.Size, "expires_at": saved.ExpiresAt}
			message = "Full tool result exceeded the response budget and was saved as a private immutable resource. Read that resource to inspect the actual outcome; do not rerun the tool."
			details["error"] = message
			return map[string]any{"isError": true, "structuredContent": details, "content": []map[string]any{
				{"type": "text", "text": message},
				{"type": "resource_link", "uri": publicartifacts.ResourceURI(saved.ArtifactID), "name": "tool-result.json", "mimeType": "application/json", "description": "Complete original tool result; authenticated read; expires in one hour."},
			}}
		}
	}
	details["error"] = message
	if err == nil {
		details["result_bytes"] = len(data)
		details["budget_bytes"] = limit
	}
	return map[string]any{"isError": true, "structuredContent": details, "content": []map[string]any{{"type": "text", "text": fmt.Sprintf("%s %s", details["code"], message)}}}
}
