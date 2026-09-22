//go:build darwin && cgo

package desktop

/*
#include "native.h"
*/
import "C"

import (
	"context"
	"encoding/json"
	"unsafe"
)

func (nativeBackend) WindowTree(ctx context.Context, w Window, nodes, depth int) (Tree, error) {
	if err := ctx.Err(); err != nil {
		return Tree{}, err
	}
	data, err := json.Marshal(w)
	if err != nil {
		return Tree{}, err
	}
	input := C.CString(string(data))
	defer C.free(unsafe.Pointer(input))
	var tree Tree
	err = decodeNative(C.ad_window_tree(input, C.int(nodes), C.int(depth)), &tree)
	return tree, err
}
func (nativeBackend) WindowInput(ctx context.Context, w Window, in WindowInput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(map[string]any{"window": w, "input": in})
	if err != nil {
		return err
	}
	input := C.CString(string(data))
	defer C.free(unsafe.Pointer(input))
	return nativeError(C.ad_window_input(input))
}

func (nativeBackend) BackgroundPointerAvailable() bool {
	return C.ad_background_pointer_supported() != 0
}
