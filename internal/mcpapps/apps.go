// Package mcpapps embeds the self-contained, content-addressed feedback views.
package mcpapps

import (
	"embed"
	"encoding/json"
	"fmt"
)

//go:embed assets/*.html assets/manifest.json
var assets embed.FS

type Resource struct {
	File     string `json:"file"`
	URI      string `json:"uri"`
	Legacy   string `json:"legacy"`
	Contract string `json:"contract"`
	SHA256   string `json:"sha256"`
	Bytes    int    `json:"bytes"`
	HTML     string `json:"-"`
}

// 仅初始化一次；每次工具发现/资源读取不重复复制所有 HTML。
var resources = loadResources()

func loadResources() map[string]Resource {
	data, err := assets.ReadFile("assets/manifest.json")
	if err != nil {
		panic(err)
	}
	result := map[string]Resource{}
	if err := json.Unmarshal(data, &result); err != nil {
		panic(err)
	}
	for view, resource := range result {
		html, err := assets.ReadFile("assets/" + resource.File)
		if err != nil {
			panic(err)
		}
		resource.HTML = string(html)
		result[view] = resource
	}
	return result
}

// HTML retains the existing Go-facing API while selecting a separate entrypoint.
// Titles belong to the localized view; the argument is not interpolated into HTML.
func HTML(view, _ string) string {
	if resource, ok := resources[view]; ok {
		return resource.HTML
	}
	panic(fmt.Sprintf("unknown MCP App view %q", view))
}
func ResourceURI(logical string) string {
	for _, resource := range resources {
		if resource.Legacy == logical || resource.URI == logical {
			return resource.URI
		}
	}
	return logical
}
func Contract(uri string) (string, bool) {
	for _, resource := range resources {
		if resource.URI == uri {
			return resource.Contract, true
		}
	}
	return "", false
}
