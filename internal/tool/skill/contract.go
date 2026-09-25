package skill

import toolcontract "github.com/uvwt/agentdock/internal/tool/contract"

const ToolPackage = "skill_package"

func PackageInputSchema() map[string]any {
	stringProp := toolcontract.String
	schema := toolcontract.InputObject(map[string]any{
		"action":    map[string]any{"type": "string", "description": "Skill package or isolated environment action.", "enum": []string{"validate", "install", "uninstall", "activate", "rollback", "env_set", "env_unset", "env_list"}},
		"skill":     stringProp("Skill name for uninstall, activate, rollback, or environment management."),
		"version":   stringProp("Installed Skill version for uninstall or activate. Omit for uninstall to remove the whole Skill."),
		"key":       stringProp("Environment variable name for env_set/env_unset."),
		"value":     stringProp("Environment variable value for env_set. Secret values are never returned."),
		"source":    stringProp("Host path or HTTP(S) URL for validate/install."),
		"digest":    stringProp("Optional expected SHA-256 digest for validate/install."),
		"activate":  toolcontract.Boolean("Activate the installed version. Defaults to true."),
		"max_bytes": toolcontract.Integer("Maximum validate/install package bytes."),
	}, "action")
	for _, a := range []string{"validate", "install"} {
		toolcontract.RequireWhen(schema, "action", a, "source")
	}
	for _, a := range []string{"uninstall", "activate", "rollback", "env_set", "env_unset", "env_list"} {
		toolcontract.RequireWhen(schema, "action", a, "skill")
	}
	toolcontract.RequireWhen(schema, "action", "activate", "version")
	toolcontract.RequireWhen(schema, "action", "env_set", "key", "value")
	toolcontract.RequireWhen(schema, "action", "env_unset", "key")
	return schema
}

func PackageOutputSchema() map[string]any {
	stringProp := toolcontract.String
	intProp := toolcontract.Integer
	boolProp := toolcontract.Boolean
	arrayProp := toolcontract.ObjectArray
	objectProp := toolcontract.OpenObject
	schema := toolcontract.OutputObject(map[string]any{
		"action":     stringProp("Completed Skill package action."),
		"skill":      stringProp("Skill name."),
		"name":       stringProp("Skill name for environment actions."),
		"key":        stringProp("Environment variable name. Secret values are never returned."),
		"configured": boolProp("Whether the environment variable has a non-empty configured value."),
		"removed":    boolProp("Whether the environment variable was removed."),
		"items":      arrayProp("Environment variable names and configured status without values."),
		"count":      intProp("Returned environment variable count."),
		"valid":      boolProp("Whether a Skill source passed validation."),
		"source":     stringProp("Resolved Skill source label."),
		"digest":     stringProp("Computed Skill package digest."),
		"issues":     arrayProp("Structured validation issues."),
		"document":   objectProp("Parsed SKILL.md frontmatter and body metadata."),
		"result":     objectProp("Install, uninstall, activate, or rollback result."),
	})
	toolcontract.Require(schema, "action")
	toolcontract.RequireWhen(schema, "action", "validate", "valid", "source", "digest", "issues")
	for _, a := range []string{"install", "uninstall", "activate", "rollback"} {
		toolcontract.RequireWhen(schema, "action", a, "result")
	}
	toolcontract.RequireWhen(schema, "action", "env_set", "name", "key", "configured")
	toolcontract.RequireWhen(schema, "action", "env_unset", "name", "key", "removed")
	toolcontract.RequireWhen(schema, "action", "env_list", "name", "items", "count")
	return schema
}

func InputSchema(name string) (map[string]any, bool) {
	if name != ToolPackage {
		return nil, false
	}
	return PackageInputSchema(), true
}

func OutputSchema(name string) (map[string]any, bool) {
	if name != ToolPackage {
		return nil, false
	}
	return PackageOutputSchema(), true
}
