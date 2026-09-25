package sourceproof

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFingerprintTracksScopeAndMutation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.go")
	_ = os.WriteFile(path, []byte("package a"), 0600)
	a := Capture(t.Context(), root, []string{"a.go"})
	b := Capture(t.Context(), root, []string{"a.go"})
	if !Matches(a, b) {
		t.Fatalf("%#v %#v", a, b)
	}
	_ = os.WriteFile(path, []byte("package b"), 0600)
	if Matches(a, Capture(t.Context(), root, []string{"a.go"})) {
		t.Fatal("mutation ignored")
	}
	if Capture(t.Context(), root, []string{"../outside"}).Complete {
		t.Fatal("escape accepted")
	}
	if Capture(t.Context(), root, nil).Complete {
		t.Fatal("non-Git implicit scope accepted")
	}
}
