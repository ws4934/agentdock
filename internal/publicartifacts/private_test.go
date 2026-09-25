package publicartifacts

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPrivateArtifactNeverGetsPublicLink(t *testing.T) {
	store := New(t.TempDir(), "https://agent.example.invalid", 8765)
	r, err := store.PublishBytes(PublishBytesRequest{Private: true, Filename: "private.bin", Data: []byte{0, 1, 2, 3}, MimeType: "application/octet-stream"})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Private || r.URL != "" {
		t.Fatalf("%#v", r)
	}
	if _, err = os.Stat(store.SecretPath); !os.IsNotExist(err) {
		t.Fatal("private publish created public signing material")
	}
	meta, data, err := store.Read(r.ArtifactID, MaxResourceBytes)
	if err != nil || !meta.Private || !bytes.Equal(data, []byte{0, 1, 2, 3}) {
		t.Fatalf("%#v %x %v", meta, data, err)
	}
	secret, err := store.ensureSecret()
	if err != nil {
		t.Fatal(err)
	}
	signed := fmt.Sprintf("https://agent.example.invalid/artifacts/public/%s/%s?expires=%d&sig=%s", r.ArtifactID, r.Filename, r.ExpiresAt.Unix(), sign(secret, r.ArtifactID, r.Filename, r.ExpiresAt.Unix(), r.SHA256))
	response := httptest.NewRecorder()
	store.ServeHTTP(response, httptest.NewRequest(http.MethodGet, signed, nil), "/artifacts/public/")
	if response.Code != http.StatusNotFound {
		t.Fatalf("private payload accessible publicly: %d", response.Code)
	}
	if got, ok := ResourceID(ResourceURI(r.ArtifactID)); !ok || got != r.ArtifactID {
		t.Fatal("resource identity did not round trip")
	}
	for _, bad := range []string{"artifact://agentdock/../private", "artifact://agentdock/not-an-id", ResourceURI(r.ArtifactID) + "?token=x"} {
		if _, ok := ResourceID(bad); ok {
			t.Fatal("malformed identity accepted")
		}
	}
}
func TestPrivateResourceIntegrityExpiryAndLimit(t *testing.T) {
	store := New(t.TempDir(), "", 0)
	r, err := store.PublishBytes(PublishBytesRequest{Private: true, Filename: "test.txt", Data: []byte("immutable"), Now: time.Now().Add(-2 * time.Hour), RetentionSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.Read(r.ArtifactID, MaxResourceBytes); err == nil {
		t.Fatal("expired resource read")
	}
	r, err = store.PublishBytes(PublishBytesRequest{Private: true, Filename: "test.txt", Data: []byte("immutable")})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.Read(r.ArtifactID, 4); err == nil {
		t.Fatal("oversize read accepted")
	}
	_ = os.WriteFile(filepath.Join(store.Root, r.ArtifactID, "payload"), []byte("mutated!!"), 0600)
	if _, _, err = store.Read(r.ArtifactID, MaxResourceBytes); err == nil {
		t.Fatal("mutated artifact read")
	}
}
