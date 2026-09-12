package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode"
)

type wordPieceTokenizer struct {
	vocab map[string]int64
	unk   int64
	cls   int64
	sep   int64
	pad   int64
}

var bertBasicSplit = regexp.MustCompile(`[^\p{L}\p{N}]+|(\p{L}+|\p{N}+)`)

func loadWordPieceTokenizer(vocabPath string) (*wordPieceTokenizer, error) {
	f, err := os.Open(vocabPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	vocab := map[string]int64{}
	sc := bufio.NewScanner(f)
	idx := int64(0)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		vocab[line] = idx
		idx++
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	t := &wordPieceTokenizer{
		vocab: vocab,
		unk:   lookupToken(vocab, "[UNK]", 100),
		cls:   lookupToken(vocab, "[CLS]", 101),
		sep:   lookupToken(vocab, "[SEP]", 102),
		pad:   lookupToken(vocab, "[PAD]", 0),
	}
	if len(vocab) == 0 {
		return nil, fmt.Errorf("empty vocab")
	}
	return t, nil
}

func lookupToken(vocab map[string]int64, token string, fallback int64) int64 {
	if id, ok := vocab[token]; ok {
		return id
	}
	return fallback
}

func (t *wordPieceTokenizer) Encode(text string, maxLen int) (inputIDs, attentionMask, tokenTypeIDs []int64) {
	if maxLen < 4 {
		maxLen = 4
	}
	tokens := []int64{t.cls}
	for _, piece := range t.basicTokenize(text) {
		for _, id := range t.wordPiece(piece) {
			tokens = append(tokens, id)
		}
	}
	tokens = append(tokens, t.sep)
	if len(tokens) > maxLen {
		tokens = tokens[:maxLen]
		tokens[maxLen-1] = t.sep
	}
	inputIDs = make([]int64, maxLen)
	attentionMask = make([]int64, maxLen)
	tokenTypeIDs = make([]int64, maxLen)
	for i := 0; i < maxLen; i++ {
		if i < len(tokens) {
			inputIDs[i] = tokens[i]
			attentionMask[i] = 1
		} else {
			inputIDs[i] = t.pad
			attentionMask[i] = 0
		}
	}
	return inputIDs, attentionMask, tokenTypeIDs
}

func (t *wordPieceTokenizer) basicTokenize(text string) []string {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return nil
	}
	parts := bertBasicSplit.FindAllString(text, -1)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.IndexFunc(p, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }) >= 0 {
			for _, r := range p {
				if unicode.IsSpace(r) {
					continue
				}
				out = append(out, string(r))
			}
			continue
		}
		out = append(out, p)
	}
	return out
}

func (t *wordPieceTokenizer) wordPiece(token string) []int64 {
	if id, ok := t.vocab[token]; ok {
		return []int64{id}
	}
	runes := []rune(token)
	start := 0
	out := []int64{}
	for start < len(runes) {
		end := len(runes)
		var found int64 = -1
		for end > start {
			sub := string(runes[start:end])
			if start > 0 {
				sub = "##" + sub
			}
			if id, ok := t.vocab[sub]; ok {
				found = id
				break
			}
			end--
		}
		if found < 0 {
			return []int64{t.unk}
		}
		out = append(out, found)
		start = end
	}
	return out
}

func meanPoolL2(hidden []float32, mask []int64, seqLen, dim int) []float32 {
	if len(hidden) < seqLen*dim {
		return nil
	}
	out := make([]float32, dim)
	var count float32
	for s := 0; s < seqLen; s++ {
		if s >= len(mask) || mask[s] == 0 {
			continue
		}
		count++
		base := s * dim
		for d := 0; d < dim; d++ {
			out[d] += hidden[base+d]
		}
	}
	if count == 0 {
		return nil
	}
	inv := 1.0 / count
	var norm float64
	for d := 0; d < dim; d++ {
		out[d] *= inv
		norm += float64(out[d]) * float64(out[d])
	}
	if norm > 0 {
		invNorm := float32(1.0 / sqrt64(norm))
		for d := 0; d < dim; d++ {
			out[d] *= invNorm
		}
	}
	return out
}

func sqrt64(v float64) float64 {
	if v <= 0 {
		return 0
	}
	x := v
	for i := 0; i < 12; i++ {
		x = 0.5 * (x + v/x)
	}
	return x
}
