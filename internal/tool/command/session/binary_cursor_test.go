package session

import (
	"bytes"
	"encoding/base64"
	"testing"
	"time"
)

func TestBinarySlicesRemainLosslesslyRecoverable(t *testing.T) {
	s := &Session{ID: "binary", StartedAt: time.Now()}
	original := []byte{0xf0, 0x9f, 0x99, 0x82, 0xff, 0x00, 0x42}
	_, _ = sessionOutputWriter{session: s}.Write(original)
	offset := int64(0)
	var received []byte
	for offset < int64(len(original)) {
		snap, err := s.SnapshotAt("running", 1, &offset, nil)
		if err != nil {
			t.Fatal(err)
		}
		data := []byte(snap.Stdout)
		if snap.StdoutEncoding == "utf-8-lossy" {
			data, err = base64.StdEncoding.DecodeString(snap.StdoutBase64)
			if err != nil {
				t.Fatal(err)
			}
		}
		received = append(received, data...)
		offset = snap.StdoutNextOffset
	}
	if !bytes.Equal(original, received) {
		t.Fatalf("bytes lost: %x", received)
	}
}
