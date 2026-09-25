package publicartifacts

import "strings"

// Resource reads reuse the MCP transport's authorization. No public URL or
// bearer credential is embedded in this identity; bytes are fetched by the host.
const ResourcePrefix = "artifact://agentdock/"
const MaxResourceBytes int64 = 64 << 20

func ResourceURI(id string) string {
	if !validArtifactID(id) {
		return ""
	}
	return ResourcePrefix + id
}
func ResourceID(uri string) (string, bool) {
	id, ok := strings.CutPrefix(uri, ResourcePrefix)
	return id, ok && validArtifactID(id)
}
