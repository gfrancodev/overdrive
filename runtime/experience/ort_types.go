package main

type ortSession struct {
	lib            uintptr
	env            uintptr
	session        uintptr
	memInfo        uintptr
	sessionOptions uintptr
	inputNames     [3]*byte
	outputName     *byte
}

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
