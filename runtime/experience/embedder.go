package main

import (
	"os"
	"strings"
	"sync"
)

type Embedder interface {
	Dim() int
	Embed(text string) []float32
	Name() string
}

type hashedEmbedder struct{}

func (hashedEmbedder) Dim() int      { return hashedDims }
func (hashedEmbedder) Name() string  { return "hashed" }
func (hashedEmbedder) Embed(text string) []float32 {
	return hashedVectorFloat32(text)
}

type stubEmbedder struct{}

func (stubEmbedder) Dim() int      { return hashedDims }
func (stubEmbedder) Name() string  { return "stub" }
func (stubEmbedder) Embed(text string) []float32 {
	v := hashedVectorFloat32(text)
	for i := range v {
		if v[i] < 0 {
			v[i] = -v[i]
		}
	}
	return v
}

type modelRunner interface {
	Run(inputIDs, attentionMask, tokenTypeIDs []int64) ([]float32, error)
}

type miniLMEmbedder struct {
	tok *wordPieceTokenizer
	ort modelRunner
}

func (miniLMEmbedder) Dim() int     { return miniLMDims }
func (miniLMEmbedder) Name() string { return "minilm" }

func (m *miniLMEmbedder) Embed(text string) []float32 {
	const maxLen = 128
	inputIDs, attentionMask, tokenTypeIDs := m.tok.Encode(text, maxLen)
	hidden, err := m.ort.Run(inputIDs, attentionMask, tokenTypeIDs)
	if err != nil {
		return hashedVectorFloat32(text)
	}
	pooled := meanPoolL2(hidden, attentionMask, maxLen, miniLMDims)
	if pooled == nil {
		return hashedVectorFloat32(text)
	}
	return pooled
}

var (
	embedOnce          sync.Once
	globalEmbedder     Embedder
	globalEmbedderName string
)

func initEmbedder(home string) {
	embedOnce.Do(func() {
		switch strings.ToLower(strings.TrimSpace(os.Getenv("OVERDRIVE_EMBEDDER"))) {
		case "stub":
			globalEmbedder = stubEmbedder{}
			globalEmbedderName = "stub"
		case "hashed":
			globalEmbedder = hashedEmbedder{}
			globalEmbedderName = "hashed"
		default:
			if emb, ok := tryMiniLM(home); ok {
				globalEmbedder = emb
				globalEmbedderName = emb.Name()
			} else {
				globalEmbedder = hashedEmbedder{}
				globalEmbedderName = "hashed"
			}
		}
	})
}

func tryMiniLM(home string) (*miniLMEmbedder, bool) {
	emb, err := buildMiniLMEmbedder(home)
	if err != nil {
		return nil, false
	}
	return emb, true
}

func buildMiniLMEmbedder(home string) (*miniLMEmbedder, error) {
	if _, err := resolveORTLibPath(home); err != nil {
		return nil, err
	}
	if err := ensureMiniLMPack(home); err != nil {
		return nil, err
	}
	tok, err := loadMiniLMTokenizer(home)
	if err != nil {
		return nil, err
	}
	sess, err := tryORTSession(home, miniLMModelPath(home))
	if err != nil {
		return nil, err
	}
	return &miniLMEmbedder{tok: tok, ort: sess}, nil
}

func loadMiniLMTokenizer(home string) (*wordPieceTokenizer, error) {
	return loadWordPieceTokenizer(miniLMVocabPath(home))
}

func currentEmbedder() (Embedder, string) {
	home, err := ensureHome()
	if err != nil {
		return hashedEmbedder{}, "hashed"
	}
	initEmbedder(home)
	return globalEmbedder, globalEmbedderName
}

func embedText(text string) []float32 {
	emb, _ := currentEmbedder()
	return emb.Embed(text)
}

func embedDim() int {
	emb, _ := currentEmbedder()
	return emb.Dim()
}

func embedderName() string {
	_, name := currentEmbedder()
	return name
}
