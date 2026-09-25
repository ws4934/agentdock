package contract

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWideIntegerDoesNotDependOnMachineInt(t *testing.T) {
	schema := BoundedInteger64("uint32 protocol identifier", 0, 4294967295)
	data, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"maximum":4294967295`) || schema["maximum"].(int64) != 4294967295 {
		t.Fatalf("truncated identifier bound: %s", data)
	}
}
