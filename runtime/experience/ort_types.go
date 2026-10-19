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

// OrtApi v18 indices (onnxruntime 1.18.1, onnxruntime_c_api.h). The table opens with CreateStatus, GetErrorCode and
// GetErrorMessage, so CreateEnv is the fourth entry.
const (
	ortIdxCreateEnv                      = 3
	ortIdxCreateSession                  = 7
	ortIdxRun                            = 9
	ortIdxCreateSessionOptions           = 10
	ortIdxCreateTensorWithDataAsOrtValue = 49
	ortIdxGetTensorMutableData           = 51
	ortIdxCreateCpuMemoryInfo            = 69
	ortIdxReleaseEnv                     = 92
	ortIdxReleaseStatus                  = 93
	ortIdxReleaseMemoryInfo              = 94
	ortIdxReleaseSession                 = 95
	ortIdxReleaseValue                   = 96
	ortIdxReleaseSessionOptions          = 100
)

const (
	onnxTensorElementDataTypeFloat = 1
	onnxTensorElementDataTypeInt64 = 7
	ortLoggingLevelError           = 3
	ortDeviceAllocator             = 0
	ortMemTypeDefault              = 0
)
