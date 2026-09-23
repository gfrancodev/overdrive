package main

const (
	version       = "1.1.0"
	backendName   = "sqlite-fts5-turbovec-v1"
	hashedDims    = 256
	miniLMDims    = 384
	vectorDims    = hashedDims
	turboBitWidth = 4
	schemaVersion = "2"

	ortAPIVersion = 18

	miniLMModelURL     = "https://huggingface.co/Xenova/all-MiniLM-L6-v2/resolve/main/onnx/model_quantized.onnx"
	miniLMVocabURL     = "https://huggingface.co/Xenova/all-MiniLM-L6-v2/resolve/main/vocab.txt"
	miniLMTokenizerURL = "https://huggingface.co/Xenova/all-MiniLM-L6-v2/resolve/main/tokenizer.json"

	ortLinuxAMD64URL = "https://github.com/microsoft/onnxruntime/releases/download/v1.18.1/onnxruntime-linux-x64-1.18.1.tgz"
	ortLinuxARM64URL = "https://github.com/microsoft/onnxruntime/releases/download/v1.18.1/onnxruntime-linux-aarch64-1.18.1.tgz"
	ortDarwinAMD64URL = "https://github.com/microsoft/onnxruntime/releases/download/v1.18.1/onnxruntime-osx-x86_64-1.18.1.tgz"
	ortDarwinARM64URL = "https://github.com/microsoft/onnxruntime/releases/download/v1.18.1/onnxruntime-osx-arm64-1.18.1.tgz"
	ortWindowsAMD64URL = "https://github.com/microsoft/onnxruntime/releases/download/v1.18.1/onnxruntime-win-x64-1.18.1.zip"
	ortWindowsARM64URL = "https://github.com/microsoft/onnxruntime/releases/download/v1.18.1/onnxruntime-win-arm64-1.18.1.zip"
)
