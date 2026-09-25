package file

import toolcontract "github.com/uvwt/agentdock/internal/tool/contract"

func batchInput(name string) (map[string]any, bool) {
	str := toolcontract.String
	integer := toolcontract.BoundedInteger
	switch name {
	case "read_files":
		item := toolcontract.InputObject(map[string]any{"path": str("Native Host path or Skill resource."), "start_line": integer("Inclusive start, default 1.", 1, 2147483200), "end_line": integer("Inclusive end; at most 400 lines per request. Default start+199.", 1, 2147483647), "expected_read_revision": str("Expected full-file revision from an earlier read.")}, "path")
		return toolcontract.InputObject(map[string]any{"requests": map[string]any{"type": "array", "minItems": 1, "maxItems": 16, "items": item}, "max_total_bytes": integer("Total returned text budget. Coalesced blocks map back to requests without duplicated content.", 1, 1<<20)}, "requests"), true
	case "search_and_read":
		return toolcontract.InputObject(map[string]any{"path": str("Native search root."), "query": str("Search query."), "regex": toolcontract.Boolean("Treat query as regex."), "case_sensitive": toolcontract.Boolean("Case-sensitive search."), "include_globs": toolcontract.StringArray("Include file patterns."), "exclude_globs": toolcontract.StringArray("Exclude patterns."), "include_hidden": toolcontract.Boolean("Include hidden paths."), "include_ignored": toolcontract.Boolean("Include ignored paths."), "context_lines": integer("Lines around each match, default 20.", 1, 80), "max_matches": integer("Matches selected for immediate reading, default 8.", 1, 16), "max_total_bytes": integer("Maximum returned source bytes.", 1, 1<<20)}, "query"), true
	}
	return nil, false
}
func batchOutput(name string) (map[string]any, bool) {
	switch name {
	case "read_files", "search_and_read":
		return toolcontract.OutputObject(map[string]any{"blocks": toolcontract.ObjectArray("Unique read blocks with request_indexes, read_revision or per-block error."), "search": toolcontract.OpenObject("Original search observation, before source reads."), "partial": toolcontract.Boolean("One or more requested blocks could not be read."), "truncated": toolcontract.Boolean("Output budget truncated one or more blocks."), "file_reads": toolcontract.Integer("Actual full-file reads after coalescing."), "returned_bytes": toolcontract.Integer("Returned text bytes."), "observation_only": toolcontract.Boolean("This operation does not modify project files.")}), true
	}
	return nil, false
}
