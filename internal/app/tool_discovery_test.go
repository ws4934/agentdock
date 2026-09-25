package app

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
)

func TestToolDiscoveryMatchesCompiledRegistryWithoutProbing(t *testing.T) {
	for _, groups := range [][]string{nil, {"core"}, {"files"}, {"execution"}} {
		t.Run(strings.Join(groups, "+"), func(t *testing.T) {
			cfg := config.Config{ToolGroups: groups}
			names, validators, err := compileAvailableToolContracts(cfg)
			if err != nil {
				t.Fatal(err)
			}
			// 故意不初始化文件、命令、网络等服务：此观察不得隐式探测或启动它们。
			r := &Runtime{cfg: cfg, toolNames: names, toolValidators: validators}
			got := r.toolDiscoveryContext()
			if got.Source != "current_core_registry" || got.RegisteredCount != len(names) || len(got.KeyTools) != 6 {
				t.Fatalf("registry observation = %+v", got)
			}
			if got.ClientVisibility != "not_observable" || got.ClientPermission != "not_observable" || got.LocalPermissions != "not_probed" {
				t.Fatalf("server registry claimed client/local permissions: %+v", got)
			}
			seen := map[string]bool{}
			for _, item := range got.KeyTools {
				if seen[item.Name] {
					t.Fatalf("duplicate key tool %s", item.Name)
				}
				seen[item.Name] = true
				d, registered := r.ToolDefinition(item.Name)
				if item.Registered != registered || (registered && item.Group != d.Group) {
					t.Fatalf("registry drift: %+v", item)
				}
			}
			if !reflect.DeepEqual(got, r.toolDiscoveryContext()) {
				t.Fatal("observation was not stable")
			}
		})
	}
}

func TestToolDiscoveryContextDiagnosticsAndOutputContractAgree(t *testing.T) {
	setUserHomeForTest(t, t.TempDir())
	r := newRuntimeValidationTestRuntime(t)
	want := r.toolDiscoveryContext()
	definition, ok := r.ToolDefinition("agentdock_context")
	if !ok {
		t.Fatal("context tool missing")
	}
	validator, err := compileBuiltInInputValidator(definition.OutputSchema)
	if err != nil {
		t.Fatal(err)
	}
	for _, local := range []bool{false, true} {
		result, err := r.agentDockContext(t.Context(), local)
		if err != nil {
			t.Fatal(err)
		}
		// 私有 Bridge 的 local context 故意省略 runtime，不套用公开工具的完整输出契约。
		if !local {
			if err := validator.ValidateValue(result, 0); err != nil {
				t.Fatalf("context output contract: %v", err)
			}
		}
		discoveryValidator, err := compileBuiltInInputValidator(toolDiscoverySchema())
		if err != nil {
			t.Fatal(err)
		}
		if err := discoveryValidator.ValidateValue(result["tool_discovery"], 0); err != nil {
			t.Fatalf("discovery output contract: %v", err)
		}
		var got capabilityContext
		if err := remarshal(result, &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got.ToolDiscovery, want) {
			t.Fatalf("context registry drift: %+v", got.ToolDiscovery)
		}
		if len(got.Rules) == 0 || got.Rules[0] != ToolDiscoveryGuidance {
			t.Fatal("bootstrap omitted discovery guidance")
		}
		if local && got.Runtime != nil {
			t.Fatal("local context duplicated runtime")
		}
		if !local {
			delete(result, "tool_discovery")
			if validator.ValidateValue(result, 0) == nil {
				t.Fatal("context accepts missing discovery evidence")
			}
		}
	}
	report := r.diagnosticResult(t.Context(), false)["diagnostics"].(diagnosticReport)
	if !reflect.DeepEqual(report.ToolDiscovery, want) || report.ToolCount != want.RegisteredCount {
		t.Fatalf("diagnostics registry drift: %+v", report.ToolDiscovery)
	}
	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > 2048 {
		t.Fatalf("bootstrap summary too large: %d", len(encoded))
	}
}

func TestToolDiscoverySchemaRejectsInventedPermissionClaims(t *testing.T) {
	validator, err := compileBuiltInInputValidator(toolDiscoverySchema())
	if err != nil {
		t.Fatal(err)
	}
	observation := (&Runtime{}).toolDiscoveryContext()
	for _, field := range []string{"client_visibility", "client_permission", "local_permissions", "unreviewed_field"} {
		var value map[string]any
		if err := remarshal(observation, &value); err != nil {
			t.Fatal(err)
		}
		if err := validator.ValidateValue(value, 0); err != nil {
			t.Fatal(err)
		}
		value[field] = "allowed"
		if validator.ValidateValue(value, 0) == nil {
			t.Fatalf("schema accepted fabricated %s", field)
		}
	}
}

func TestCatalogExactNameExportsOneFullContract(t *testing.T) {
	cfg := config.Config{MCPAppsEnabled: true}
	for _, name := range []string{"exec_command", "file_edit", "tool_catalog"} {
		for _, format := range []string{"summary", "mcp", "openai"} {
			t.Run(name+"/"+format, func(t *testing.T) {
				result, err := ExportToolCatalog(cfg, CatalogRequest{Format: format, Name: name})
				if err != nil {
					t.Fatal(err)
				}
				if result["count"] != 1 || result["observation_only"] != true {
					t.Fatalf("%+v", result)
				}
				groups := result["groups"].([]map[string]any)
				if len(groups) != 1 || groups[0]["count"] != 1 {
					t.Fatal("wrong group count")
				}
				tools := result["tools"].([]map[string]any)
				if format == "openai" {
					if len(tools) != 2 || tools[1]["type"] != "tool_search" {
						t.Fatal("incorrect namespace export")
					}
					tools = tools[0]["tools"].([]map[string]any)
				}
				if len(tools) != 1 || tools[0]["name"] != name {
					t.Fatal("exact filter leaked other tools")
				}
				if format == "mcp" {
					d, _ := toolDefinitionForConfig(name, cfg)
					if !reflect.DeepEqual(tools[0], MCPToolDescriptor(d, true)) {
						t.Fatal("descriptor was weakened or changed")
					}
				}
				again, err := ExportToolCatalog(cfg, CatalogRequest{Format: format, Name: name, Group: groups[0]["id"].(string)})
				if err != nil || again["catalog_revision"] != result["catalog_revision"] {
					t.Fatal("same selection changed catalog revision")
				}
			})
		}
	}
}

func TestCatalogExactNameCannotEnableOrInvokeTools(t *testing.T) {
	setUserHomeForTest(t, t.TempDir())
	cfg := config.Config{AgentDockDefaultDir: t.TempDir(), AgentDockHome: filepath.Join(t.TempDir(), ".agentdock"), ToolGroups: []string{"files"}}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	r, err := NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	before := r.toolDiscoveryContext()
	for _, args := range []map[string]any{
		{"format": "mcp", "name": "exec_command"},
		{"format": "mcp", "name": "not_a_tool"},
		{"format": "mcp", "group": "execution", "name": "file_edit"},
		{"format": "mcp", "name": ""},
		{"format": "mcp", "name": "file_edit", "cmd": "must-not-run"},
	} {
		if _, err := r.Call(t.Context(), "tool_catalog", args); err == nil {
			t.Fatalf("catalog accepted %v", args)
		}
	}
	result, err := r.Call(t.Context(), "tool_catalog", map[string]any{"format": "mcp", "name": "file_edit"})
	if err != nil || result["count"] != 1 {
		t.Fatalf("available edit schema: %v %v", result, err)
	}
	if _, err := r.Call(t.Context(), "exec_command", map[string]any{"cmd": "must-not-run"}); err == nil {
		t.Fatal("disabled command enabled")
	}
	if _, err := r.CallNamespaced(t.Context(), "execution", "exec_command", map[string]any{"cmd": "must-not-run"}); err == nil {
		t.Fatal("namespace bypassed disabled group")
	}
	if !reflect.DeepEqual(before, r.toolDiscoveryContext()) {
		t.Fatal("discovery changed capabilities")
	}
	for _, name := range []string{"exec_command", "file_edit"} {
		d, _ := toolDefinitionForConfig(name, config.Config{})
		if d.Annotations == nil || d.Annotations.ReadOnlyHint {
			t.Fatalf("%s falsely advertised as read-only", name)
		}
	}
}
