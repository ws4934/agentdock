package contract

// 约束由能力包声明，公共工具只负责构造 JSON Schema，不按工具名称分派。
func AddConstraint(schema map[string]any, rule map[string]any) {
	rules := []any{}
	switch old := schema["allOf"].(type) {
	case []any:
		rules = append(rules, old...)
	case []map[string]any:
		for _, r := range old {
			rules = append(rules, r)
		}
	}
	schema["allOf"] = append(rules, rule)
}
func RequireWhen(schema map[string]any, key string, value any, fields ...string) {
	AddConstraint(schema, map[string]any{"if": map[string]any{"required": []string{key}, "properties": map[string]any{key: map[string]any{"const": value}}}, "then": map[string]any{"required": fields}})
}
func ForbidTogether(schema map[string]any, fields ...string) {
	AddConstraint(schema, map[string]any{"not": map[string]any{"required": fields}})
}
func Require(schema map[string]any, fields ...string) {
	list := []string{}
	seen := map[string]bool{}
	switch old := schema["required"].(type) {
	case []string:
		list = append(list, old...)
	case []any:
		for _, r := range old {
			if v, ok := r.(string); ok {
				list = append(list, v)
			}
		}
	}
	for _, f := range list {
		seen[f] = true
	}
	for _, f := range fields {
		if !seen[f] {
			list = append(list, f)
			seen[f] = true
		}
	}
	schema["required"] = list
}
func Variants(schema map[string]any, variants ...[]string) {
	alternatives := []map[string]any{}
	for _, fields := range variants {
		alternatives = append(alternatives, map[string]any{"required": fields})
	}
	AddConstraint(schema, map[string]any{"anyOf": alternatives})
}
