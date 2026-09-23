package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

var hookBytePtrFromString = syscall.BytePtrFromString

func validateORTTokenTensors(inputIDs, attentionMask, tokenTypeIDs []int64) error {
	seqLen := len(inputIDs)
	if seqLen == 0 || len(attentionMask) != seqLen || len(tokenTypeIDs) != seqLen {
		return fmt.Errorf("invalid token tensors")
	}
	return nil
}

func ortInt64TensorShape(seqLen int) []int64 {
	return []int64{1, int64(seqLen)}
}

func ortHiddenOutputFloatCount(seqLen int) int {
	return seqLen * miniLMDims
}

func readORTGetAPIAddr(base uintptr) uintptr {
	if base == 0 {
		return 0
	}
	return *(*uintptr)(unsafe.Pointer(base))
}

func resolveORTAPI(getApiBase func() uintptr, version int) (uintptr, error) {
	if getApiBase == nil {
		return 0, fmt.Errorf("OrtGetApiBase missing")
	}
	base := getApiBase()
	if base == 0 {
		return 0, fmt.Errorf("OrtGetApiBase returned nil")
	}
	getApi := readORTGetAPIAddr(base)
	if getApi == 0 {
		return 0, fmt.Errorf("GetApi missing")
	}
	api, _, _ := ortSyscallN(getApi, uintptr(version))
	if api == 0 {
		return 0, fmt.Errorf("unsupported ort api version %d", version)
	}
	return api, nil
}

func bindORTSessionTensorNames(s *ortSession) error {
	names := []struct {
		dst **byte
		val string
	}{
		{&s.inputNames[0], "input_ids"},
		{&s.inputNames[1], "attention_mask"},
		{&s.inputNames[2], "token_type_ids"},
		{&s.outputName, "last_hidden_state"},
	}
	for _, n := range names {
		ptr, err := hookBytePtrFromString(n.val)
		if err != nil {
			return err
		}
		*n.dst = ptr
	}
	return nil
}

func copyFloat32FromUnsafe(ptr uintptr, n int) []float32 {
	out := make([]float32, n)
	if ptr == 0 || n == 0 {
		return out
	}
	src := unsafe.Slice((*float32)(unsafe.Pointer(ptr)), n)
	copy(out, src)
	return out
}

func releaseORTValues(vals []uintptr, releaseFn uintptr) {
	for _, v := range vals {
		if v != 0 && releaseFn != 0 {
			ortCall(releaseFn, v)
		}
	}
}
