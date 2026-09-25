package mcp

import (
	"reflect"
	"testing"
)

func TestWorkResultResourceAdvertisesNegotiatedDisplayModes(t *testing.T) {
	definition := appResourceDefinition{Name: "agentdock-work-result", URI: "ui://synthetic/work"}
	meta := appResourceMetaForDefinition(definition, "https://synthetic.example")
	display := meta["openai/ui"].(map[string]any)["availableDisplayModes"]
	if !reflect.DeepEqual(display, []string{"inline", "fullscreen", "pip"}) {
		t.Fatalf("display modes: %#v", display)
	}
	result := appResourceReadResult(definition, "https://synthetic.example")
	if !reflect.DeepEqual(result.Contents[0].Meta, meta) {
		t.Fatal("resource descriptor and content metadata differ")
	}
	other := appResourceMetaForDefinition(appResourceDefinition{Name: "agentdock-context"}, "")
	if _, exists := other["openai/ui"]; exists {
		t.Fatal("unrelated views should not advertise task controls")
	}
	if _, exists := other["ui"]; !exists {
		t.Fatal("standard MCP metadata lost")
	}
}
