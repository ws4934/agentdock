package app

import (
	"context"
	"testing"
)

func TestHandlerPanicProducesUnconfirmedFailure(t *testing.T) {
	result, err := invokeToolHandler(t.Context(), nil, func(context.Context, *Runtime, map[string]any) (Result, error) { panic("SYNTHETIC_PRIVATE") }, nil)
	if result != nil || err == nil {
		t.Fatal("panic became success")
	}
	e, ok := err.(*ToolError)
	if !ok || e.Code != "TOOL_PANIC" || e.Retryable {
		t.Fatalf("%+v", err)
	}
}
