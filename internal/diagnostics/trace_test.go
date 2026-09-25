package diagnostics

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

func TestTraceIsBoundedAndMetadataOnly(t *testing.T) {
	r := New([]string{"read_file"})
	var group sync.WaitGroup
	for i := 0; i < 500; i++ {
		group.Go(func() { finish := r.Start("read_file"); finish(true, "OK"); finish(false, "IGNORED") })
	}
	group.Wait()
	snapshot := r.Snapshot()
	if len(snapshot) != 128 {
		t.Fatalf("events=%d", len(snapshot))
	}
	for _, event := range snapshot {
		if event.Status != "completed" || event.RPCSuccess == nil || !*event.RPCSuccess || event.Code != "OK" || event.ResponseDelivery != "not_observed" {
			t.Fatalf("%#v", event)
		}
	}
	end := r.Start("SECRET-CALLER-NAME")
	end(false, "secret contents with spaces")
	data, _ := json.Marshal(r.Snapshot())
	if strings.Contains(string(data), "SECRET-CALLER-NAME") || strings.Contains(string(data), "secret contents") {
		t.Fatal("unbounded caller text was retained")
	}
}
