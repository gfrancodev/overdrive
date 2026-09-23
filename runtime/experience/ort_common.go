package main

import (
	"fmt"
	"path/filepath"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

var (
	ortLibOnce sync.Once
	ortLibPath string
	ortLibErr  error

	ortSessionOpener   = openORTSessionFFI
	ortSyscallN        = purego.SyscallN
	ortReleaseStatusFn = releaseORTStatus
	ortAPIFuncAt       = ortAPIFuncUnsafe
)

func ortAPIFuncUnsafe(api uintptr, idx int) uintptr {
	if api == 0 {
		return 0
	}
	return *(*uintptr)(unsafe.Pointer(api + uintptr(idx)*unsafe.Sizeof(uintptr(0))))
}

func releaseORTStatus(api, status uintptr) {
	if api == 0 {
		return
	}
	release := ortAPIFuncAt(api, ortIdxReleaseStatus)
	ortCall(release, status)
}

func resetORTLibForTest() {
	ortLibOnce = sync.Once{}
	ortLibPath = ""
	ortLibErr = nil
}

func resolveORTLibPath(home string) (string, error) {
	ortLibOnce.Do(func() {
		ortLibPath, ortLibErr = ensureORTLib(home)
	})
	return ortLibPath, ortLibErr
}

func tryORTSession(home, modelPath string) (modelRunner, error) {
	if hookTryORTSession != nil {
		return hookTryORTSession(home, modelPath)
	}
	libPath, err := resolveORTLibPath(home)
	if err != nil {
		return nil, err
	}
	return ortSessionOpener(libPath, modelPath)
}

func miniLMModelPath(home string) string {
	return filepath.Join(modelDir(home), "model.onnx")
}

func miniLMVocabPath(home string) string {
	return filepath.Join(modelDir(home), "vocab.txt")
}

func ortStatusError(api, status uintptr) error {
	if status == 0 {
		return nil
	}
	if api != 0 {
		ortReleaseStatusFn(api, status)
	}
	return fmt.Errorf("onnxruntime call failed")
}

func apiFn(api uintptr, idx int) uintptr {
	return ortAPIFuncAt(api, idx)
}

func ortCall(fn uintptr, args ...uintptr) uintptr {
	if fn == 0 {
		return 1
	}
	all := append([]uintptr{fn}, args...)
	ret, _, _ := ortSyscallN(all[0], all[1:]...)
	return ret
}
