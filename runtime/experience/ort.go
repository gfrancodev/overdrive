package main

import (
	"fmt"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"
)

// OrtApi v18 indices (onnxruntime 1.18.1).
const (
	ortIdxCreateEnv                      = 0
	ortIdxCreateSession                  = 4
	ortIdxRun                            = 6
	ortIdxCreateSessionOptions           = 7
	ortIdxCreateTensorWithDataAsOrtValue = 46
	ortIdxGetTensorMutableData           = 48
	ortIdxCreateCpuMemoryInfo            = 66
	ortIdxReleaseStatus                  = 90
	ortIdxReleaseEnv                     = 89
	ortIdxReleaseSession                 = 92
	ortIdxReleaseValue                   = 93
	ortIdxReleaseSessionOptions          = 97
)

const (
	onnxTensorElementDataTypeFloat = 1
	onnxTensorElementDataTypeInt64 = 7
	ortLoggingLevelWarning         = 3
	ortDeviceAllocator             = 0
	ortMemTypeDefault              = 0
)

type ortSession struct {
	lib            uintptr
	env            uintptr
	session        uintptr
	memInfo        uintptr
	sessionOptions uintptr
	inputNames     [3]*byte
	outputName     *byte
}

var (
	ortLibOnce sync.Once
	ortLibPath string
	ortLibErr  error
)

func newORTSession(libPath, modelPath string) (*ortSession, error) {
	lib, err := openDynamicLib(libPath)
	if err != nil {
		return nil, err
	}
	var getApiBase func() uintptr
	purego.RegisterLibFunc(&getApiBase, lib, "OrtGetApiBase")
	if getApiBase == nil {
		return nil, fmt.Errorf("OrtGetApiBase missing")
	}
	base := getApiBase()
	if base == 0 {
		return nil, fmt.Errorf("OrtGetApiBase returned nil")
	}
	getApi := *(*uintptr)(unsafe.Pointer(base))
	if getApi == 0 {
		return nil, fmt.Errorf("GetApi missing")
	}
	api, _, _ := purego.SyscallN(getApi, uintptr(ortAPIVersion))
	if api == 0 {
		return nil, fmt.Errorf("unsupported ort api version %d", ortAPIVersion)
	}

	s := &ortSession{lib: lib}
	if err := s.init(api, modelPath); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *ortSession) init(api uintptr, modelPath string) error {
	createEnv := apiFn(api, ortIdxCreateEnv)
	createSessionOptions := apiFn(api, ortIdxCreateSessionOptions)
	createSession := apiFn(api, ortIdxCreateSession)
	createCpuMemoryInfo := apiFn(api, ortIdxCreateCpuMemoryInfo)

	logid, err := syscall.BytePtrFromString("overdrive")
	if err != nil {
		return err
	}
	if status := ortCall(createEnv, ortLoggingLevelWarning, uintptr(unsafe.Pointer(logid)), uintptr(unsafe.Pointer(&s.env))); status != 0 {
		return ortStatusError(api, status)
	}
	if status := ortCall(createSessionOptions, uintptr(unsafe.Pointer(&s.sessionOptions))); status != 0 {
		return ortStatusError(api, status)
	}
	modelC, err := syscall.BytePtrFromString(modelPath)
	if err != nil {
		return err
	}
	if status := ortCall(createSession, s.env, uintptr(unsafe.Pointer(modelC)), s.sessionOptions, uintptr(unsafe.Pointer(&s.session))); status != 0 {
		return ortStatusError(api, status)
	}
	if status := ortCall(createCpuMemoryInfo, ortDeviceAllocator, ortMemTypeDefault, uintptr(unsafe.Pointer(&s.memInfo))); status != 0 {
		return ortStatusError(api, status)
	}

	s.inputNames[0], _ = syscall.BytePtrFromString("input_ids")
	s.inputNames[1], _ = syscall.BytePtrFromString("attention_mask")
	s.inputNames[2], _ = syscall.BytePtrFromString("token_type_ids")
	s.outputName, _ = syscall.BytePtrFromString("last_hidden_state")
	return nil
}

func (s *ortSession) Run(inputIDs, attentionMask, tokenTypeIDs []int64) ([]float32, error) {
	seqLen := len(inputIDs)
	if seqLen == 0 || len(attentionMask) != seqLen || len(tokenTypeIDs) != seqLen {
		return nil, fmt.Errorf("invalid token tensors")
	}
	api := s.ortAPI()
	if api == 0 {
		return nil, fmt.Errorf("ort api unavailable")
	}
	createTensor := apiFn(api, ortIdxCreateTensorWithDataAsOrtValue)
	run := apiFn(api, ortIdxRun)
	getMutable := apiFn(api, ortIdxGetTensorMutableData)
	releaseValue := apiFn(api, ortIdxReleaseValue)

	shape := []int64{1, int64(seqLen)}
	var values [3]uintptr
	tensors := make([]uintptr, 3)
	data := [][]int64{inputIDs, attentionMask, tokenTypeIDs}
	for i := 0; i < 3; i++ {
		dataPtr := uintptr(unsafe.Pointer(&data[i][0]))
		if status := ortCall(
			createTensor,
			s.memInfo,
			dataPtr,
			uintptr(len(data[i])*8),
			uintptr(unsafe.Pointer(&shape[0])),
			2,
			onnxTensorElementDataTypeInt64,
			uintptr(unsafe.Pointer(&tensors[i])),
		); status != 0 {
			s.releaseValues(tensors[:i], releaseValue)
			return nil, ortStatusError(api, status)
		}
		values[i] = tensors[i]
	}
	defer s.releaseValues(tensors[:], releaseValue)

	var outputTensor uintptr
	in0 := s.inputNames[0]
	in1 := s.inputNames[1]
	in2 := s.inputNames[2]
	namePtrs := [3]*byte{in0, in1, in2}
	if status := ortCall(
		run,
		s.session,
		0,
		uintptr(unsafe.Pointer(&namePtrs[0])),
		uintptr(unsafe.Pointer(&values[0])),
		3,
		uintptr(unsafe.Pointer(&s.outputName)),
		1,
		uintptr(unsafe.Pointer(&outputTensor)),
	); status != 0 {
		return nil, ortStatusError(api, status)
	}
	defer ortCall(releaseValue, outputTensor)

	var dataPtr uintptr
	if status := ortCall(getMutable, outputTensor, uintptr(unsafe.Pointer(&dataPtr))); status != 0 {
		return nil, ortStatusError(api, status)
	}
	hiddenDim := miniLMDims
	total := seqLen * hiddenDim
	out := make([]float32, total)
	src := unsafe.Slice((*float32)(unsafe.Pointer(dataPtr)), total)
	copy(out, src)
	return out, nil
}

func (s *ortSession) Close() {
	api := s.ortAPI()
	if api == 0 {
		return
	}
	if s.session != 0 {
		releaseSession := apiFn(api, ortIdxReleaseSession)
		ortCall(releaseSession, s.session)
		s.session = 0
	}
	if s.sessionOptions != 0 {
		releaseOpts := apiFn(api, ortIdxReleaseSessionOptions)
		ortCall(releaseOpts, s.sessionOptions)
		s.sessionOptions = 0
	}
	if s.memInfo != 0 {
		// memory info released with env in minimal binding
		s.memInfo = 0
	}
	if s.env != 0 {
		releaseEnv := apiFn(api, ortIdxReleaseEnv)
		ortCall(releaseEnv, s.env)
		s.env = 0
	}
}

func (s *ortSession) ortAPI() uintptr {
	var getApiBase func() uintptr
	purego.RegisterLibFunc(&getApiBase, s.lib, "OrtGetApiBase")
	if getApiBase == nil {
		return 0
	}
	base := getApiBase()
	getApi := *(*uintptr)(unsafe.Pointer(base))
	api, _, _ := purego.SyscallN(getApi, uintptr(ortAPIVersion))
	return api
}

func (s *ortSession) releaseValues(vals []uintptr, releaseFn uintptr) {
	for _, v := range vals {
		if v != 0 {
			ortCall(releaseFn, v)
		}
	}
}

func apiFn(api uintptr, idx int) uintptr {
	return *(*uintptr)(unsafe.Pointer(api + uintptr(idx)*unsafe.Sizeof(uintptr(0))))
}

func ortCall(fn uintptr, args ...uintptr) uintptr {
	all := append([]uintptr{fn}, args...)
	ret, _, _ := purego.SyscallN(all[0], all[1:]...)
	return ret
}

func ortStatusError(api, status uintptr) error {
	if status == 0 {
		return nil
	}
	releaseStatus := apiFn(api, ortIdxReleaseStatus)
	ortCall(releaseStatus, status)
	return fmt.Errorf("onnxruntime call failed")
}

func resolveORTLibPath(home string) (string, error) {
	ortLibOnce.Do(func() {
		ortLibPath, ortLibErr = ensureORTLib(home)
	})
	return ortLibPath, ortLibErr
}

func tryORTSession(home, modelPath string) (*ortSession, error) {
	libPath, err := resolveORTLibPath(home)
	if err != nil {
		return nil, err
	}
	return newORTSession(libPath, modelPath)
}

func miniLMModelPath(home string) string {
	return filepath.Join(modelDir(home), "model.onnx")
}

func miniLMVocabPath(home string) string {
	return filepath.Join(modelDir(home), "vocab.txt")
}
