//go:build !overdrive_fake_ort

package main

import (
	"fmt"
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"
)

func openORTSessionFFI(libPath, modelPath string) (modelRunner, error) {
	lib, err := openDynamicLib(libPath)
	if err != nil {
		return nil, err
	}
	var getApiBase func() uintptr
	purego.RegisterLibFunc(&getApiBase, lib, "OrtGetApiBase")
	api, err := resolveORTAPI(getApiBase, ortAPIVersion)
	if err != nil {
		return nil, err
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
	return bindORTSessionTensorNames(s)
}

func (s *ortSession) Run(inputIDs, attentionMask, tokenTypeIDs []int64) ([]float32, error) {
	if err := validateORTTokenTensors(inputIDs, attentionMask, tokenTypeIDs); err != nil {
		return nil, err
	}
	seqLen := len(inputIDs)
	api := s.ortAPI()
	if api == 0 {
		return nil, fmt.Errorf("ort api unavailable")
	}
	createTensor := apiFn(api, ortIdxCreateTensorWithDataAsOrtValue)
	run := apiFn(api, ortIdxRun)
	getMutable := apiFn(api, ortIdxGetTensorMutableData)
	releaseValue := apiFn(api, ortIdxReleaseValue)

	shape := ortInt64TensorShape(seqLen)
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
			releaseORTValues(tensors[:i], releaseValue)
			return nil, ortStatusError(api, status)
		}
		values[i] = tensors[i]
	}
	defer releaseORTValues(tensors[:], releaseValue)

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
	return copyFloat32FromUnsafe(dataPtr, ortHiddenOutputFloatCount(seqLen)), nil
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
	api, err := resolveORTAPI(getApiBase, ortAPIVersion)
	if err != nil {
		return 0
	}
	return api
}
