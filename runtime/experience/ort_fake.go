//go:build overdrive_fake_ort

package main

import "fmt"

type fakeORTSession struct {
	modelPath string
	closed    bool
}

func openORTSessionFFI(libPath, modelPath string) (modelRunner, error) {
	if libPath == "" {
		return nil, fmt.Errorf("ort library missing")
	}
	return &fakeORTSession{modelPath: modelPath}, nil
}

func (f *fakeORTSession) Run(inputIDs, attentionMask, tokenTypeIDs []int64) ([]float32, error) {
	if err := validateORTTokenTensors(inputIDs, attentionMask, tokenTypeIDs); err != nil {
		return nil, err
	}
	out := make([]float32, ortHiddenOutputFloatCount(len(inputIDs)))
	for i := range out {
		out[i] = 0.01
	}
	return out, nil
}

func (f *fakeORTSession) Close() {
	f.closed = true
}
