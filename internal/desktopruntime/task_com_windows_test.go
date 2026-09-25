//go:build windows

package desktopruntime

import (
	"testing"
	"unsafe"
)

func TestTaskCOMVariantABIAndArrayStride(t *testing.T) {
	word := unsafe.Sizeof(uintptr(0))
	expected := uintptr(8) + 2*word
	var value variant
	if unsafe.Sizeof(value) != expected || unsafe.Offsetof(value.Val) != 8 {
		t.Fatalf("VARIANT ABI size=%d val-offset=%d, want size=%d offset=8", unsafe.Sizeof(value), unsafe.Offsetof(value.Val), expected)
	}
	values := [4]variant{variantEmpty(), variantInt32(1), variantInt32(42), variantEmpty()}
	if uintptr(unsafe.Pointer(&values[1]))-uintptr(unsafe.Pointer(&values[0])) != expected {
		t.Fatal("VARIANTARG array has incompatible native stride")
	}
	if variantInt(variantInt32(-7)) != -7 || variantInt(variantBool(true)) != variantTrue {
		t.Fatal("scalar value representation changed")
	}
	var params dispParams
	if unsafe.Sizeof(params) != 2*word+8 || unsafe.Offsetof(params.cArgs) != 2*word {
		t.Fatal("DISPPARAMS ABI changed")
	}
}

func TestTaskCOMProceduresResolve(t *testing.T) {
	for _, test := range []struct {
		name string
		find func() error
	}{
		{name: "CoInitializeEx", find: procCoInitializeEx.Find},
		{name: "CoUninitialize", find: procCoUninitialize.Find},
		{name: "CoCreateInstance", find: procCoCreateInstance.Find},
		{name: "CLSIDFromProgID", find: procCLSIDFromProgID.Find},
		{name: "SysAllocString", find: procSysAllocString.Find},
		{name: "SysFreeString", find: procSysFreeString.Find},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.find(); err != nil {
				t.Fatalf("resolve Windows COM procedure %s: %v", test.name, err)
			}
		})
	}
}
