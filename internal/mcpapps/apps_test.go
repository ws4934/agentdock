package mcpapps

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

func TestEmbeddedViewsAreContentAddressed(t *testing.T) {
	if len(resources) != 9 {
		t.Fatalf("views=%d", len(resources))
	}
	seen := map[string]bool{}
	for view, r := range resources {
		if seen[r.URI] {
			t.Fatal("duplicate URI")
		}
		seen[r.URI] = true
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(r.HTML)))
		if r.SHA256 != hash || !strings.Contains(r.URI, "/v2-"+hash[:16]+".html") {
			t.Fatalf("hash mismatch %s", view)
		}
		if r.Bytes != len(r.HTML) || r.Bytes > 512*1024 {
			t.Fatalf("invalid size %s: %d", view, len(r.HTML))
		}
		if HTML(view, "") != r.HTML || ResourceURI(r.Legacy) != r.URI {
			t.Fatal("lookup mismatch")
		}
		if contract, ok := Contract(r.URI); !ok || contract != r.Contract {
			t.Fatal("contract mismatch")
		}
		for _, forbidden := range []string{"<script src=", "<link rel=\"stylesheet\"", "unsafe-eval", "http-equiv=\"refresh\""} {
			if strings.Contains(r.HTML, forbidden) {
				t.Fatalf("external/unsafe resource in %s: %s", view, forbidden)
			}
		}
		if !strings.Contains(r.HTML, "connect-src 'none'") || !strings.Contains(r.HTML, `name="agentdock-view" content="`+view+`"`) {
			t.Fatalf("resource metadata missing %s", view)
		}
	}
	if _, ok := Contract("ui://agentdock/context/v2-forged.html"); ok {
		t.Fatal("accepted unknown revision")
	}
}
func BenchmarkHTMLLookup(b *testing.B) {
	for b.Loop() {
		_ = HTML("agentdock_context", "")
	}
}
