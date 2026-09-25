package jobrun

import (
	"runtime"
	"sort"
	"strings"
)

// executionEnvironment uses the same last-value-wins semantics for the
// idempotency fingerprint and the child. Raw sorting would conflate duplicates.
func executionEnvironment(env []string) []string {
	values := map[string]string{}
	for _, item := range env {
		start := 0
		if runtime.GOOS == "windows" && strings.HasPrefix(item, "=") {
			start = 1
		}
		index := strings.IndexByte(item[start:], '=')
		if index < 0 {
			continue
		}
		key := item[:start+index]
		if runtime.GOOS == "windows" {
			key = strings.ToUpper(key)
		}
		values[key] = item
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, values[key])
	}
	return out
}
