package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/ebitengine/purego"
)

type TurboVecIndex struct {
	handle    uintptr
	path      string
	dim       int
	available bool
}

var (
	tvOnce       sync.Once
	tvLoaded     bool
	tvOpen       func(*byte, int32, int32) uintptr
	tvClose      func(uintptr)
	tvAdd        func(uintptr, uint64, *float32, int32) int32
	tvRemove     func(uintptr, uint64) int32
	tvSearch     func(uintptr, *float32, int32, int32, *uint64, int32, *uint64, *float32, *int32) int32
	tvSync       func(uintptr) int32
)

var turbovecLibNames = map[string]string{
	"darwin":  "liboverdrive_turbovec_ffi.dylib",
	"windows": "overdrive_turbovec_ffi.dll",
}

func turbovecLibName() string {
	if name, ok := turbovecLibNames[runtimePlatformOS()]; ok {
		return name
	}
	return "liboverdrive_turbovec_ffi.so"
}

func resolveTurboVecLib(home string) string {
	candidates := []string{}
	if v := strings.TrimSpace(os.Getenv("OVERDRIVE_TURBOVEC_LIB")); v != "" {
		candidates = append(candidates, v)
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), turbovecLibName()))
	}
	candidates = append(candidates, filepath.Join(libDir(home), turbovecLibName()))
	repoLib := filepath.Join("runtime", "turbovec-ffi", "target", "release", turbovecLibName())
	candidates = append(candidates, repoLib)
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(wd, repoLib))
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

func loadTurboVecLib(home string) bool {
	tvOnce.Do(func() {
		path := resolveTurboVecLib(home)
		if path == "" {
			return
		}
		lib, err := openDynamicLib(path)
		if err != nil {
			return
		}
		purego.RegisterLibFunc(&tvOpen, lib, "od_tv_open")
		purego.RegisterLibFunc(&tvClose, lib, "od_tv_close")
		purego.RegisterLibFunc(&tvAdd, lib, "od_tv_add")
		purego.RegisterLibFunc(&tvRemove, lib, "od_tv_remove")
		purego.RegisterLibFunc(&tvSearch, lib, "od_tv_search")
		purego.RegisterLibFunc(&tvSync, lib, "od_tv_sync")
		tvLoaded = tvOpen != nil
	})
	return tvLoaded
}

func openTurboVec(home string, dim int) *TurboVecIndex {
	return openTurboVecAt(vectorIndexPath(home), home, dim)
}

func openTurboVecAt(path string, home string, dim int) *TurboVecIndex {
	if !loadTurboVecLib(home) {
		return &TurboVecIndex{available: false, dim: dim}
	}
	cPath, err := syscall.BytePtrFromString(path)
	if err != nil {
		return &TurboVecIndex{available: false, dim: dim}
	}
	handle := tvOpen(cPath, int32(dim), int32(turboBitWidth))
	if handle == 0 {
		return &TurboVecIndex{available: false, dim: dim, path: path}
	}
	return &TurboVecIndex{
		handle:    handle,
		path:      path,
		dim:       dim,
		available: true,
	}
}

func (tv *TurboVecIndex) Close() {
	if tv == nil || !tv.available || tv.handle == 0 || tvClose == nil {
		return
	}
	tvClose(tv.handle)
	tv.handle = 0
}

func (tv *TurboVecIndex) Available() bool {
	return tv != nil && tv.available && tv.handle != 0
}

func (tv *TurboVecIndex) Add(id string, vector []float32) {
	if !tv.Available() || len(vector) != tv.dim {
		return
	}
	_ = tvAdd(tv.handle, memoryVectorID(id), &vector[0], int32(tv.dim))
}

func (tv *TurboVecIndex) Remove(id string) {
	if !tv.Available() {
		return
	}
	_ = tvRemove(tv.handle, memoryVectorID(id))
}

func (tv *TurboVecIndex) Search(query []float32, k int, allowlist []uint64) (ids []uint64, scores []float32) {
	if !tv.Available() || len(query) != tv.dim || k <= 0 {
		return nil, nil
	}
	outIDs := make([]uint64, k)
	outScores := make([]float32, k)
	var outCount int32
	var allowPtr *uint64
	allowLen := int32(0)
	if len(allowlist) > 0 {
		allowPtr = &allowlist[0]
		allowLen = int32(len(allowlist))
	}
	rc := tvSearch(tv.handle, &query[0], int32(tv.dim), int32(k), allowPtr, allowLen, &outIDs[0], &outScores[0], &outCount)
	if rc != 0 || outCount <= 0 {
		return nil, nil
	}
	return outIDs[:outCount], outScores[:outCount]
}

func (tv *TurboVecIndex) Sync() {
	if !tv.Available() || tvSync == nil {
		return
	}
	_ = tvSync(tv.handle)
}

func installBundledTurboVecLib(home string) {
	src := resolveTurboVecLib(home)
	if src == "" {
		return
	}
	dst := filepath.Join(libDir(home), turbovecLibName())
	if st, err := os.Stat(dst); err == nil && st.Size() > 0 {
		return
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return
	}
	_ = os.WriteFile(dst, data, 0o755)
}

func normalizeVectorScore(score float32) float64 {
	if score <= 0 {
		return 0
	}
	return clamp(float64(score)/10.0, 0, 1)
}
