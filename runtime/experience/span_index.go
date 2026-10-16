package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var errSpanOutOfRange = errors.New("span out of range")

var spanReadCount int

type spanRow struct {
	Repository string
	Harness    string
	Path       string
	ByteStart  int64
	ByteEnd    int64
	Signature  string
	Title      string
}

func spanBoundsOK(fileSize, start, end int64) error {
	if start < 0 || end < start || end > fileSize {
		return errSpanOutOfRange
	}
	return nil
}

func decisionShaped(line string) bool {
	l := strings.ToLower(strings.TrimSpace(line))
	if l == "" {
		return false
	}
	for _, needle := range []string{"decid", "vamos usar", "não cria", "nao cria", "sem segunda", "we'll use", "do not add"} {
		if strings.Contains(l, needle) {
			return true
		}
	}
	return false
}

func shouldOpenTranscript(decisions []Memory, query string) bool {
	if strings.TrimSpace(query) == "" {
		return false
	}
	if decisionCoversQuery(decisions, query) {
		return false
	}
	return true
}

func decisionMemoryMatchesQuery(m Memory, q string) bool {
	if m.Kind != "decision" {
		return false
	}
	if m.Status != "" && m.Status != "active" {
		return false
	}
	blob := strings.ToLower(m.Subject + " " + m.Content)
	subject := strings.ToLower(strings.TrimSpace(m.Subject))
	if subject != "" && strings.Contains(q, subject) {
		return true
	}
	if strings.Contains(blob, q) {
		return true
	}
	for _, tok := range strings.Fields(q) {
		if len(tok) >= 4 && strings.Contains(blob, tok) {
			return true
		}
	}
	return false
}

func decisionCoversQuery(decisions []Memory, query string) bool {
	q := strings.ToLower(query)
	for _, m := range decisions {
		if decisionMemoryMatchesQuery(m, q) {
			return true
		}
	}
	return false
}

func spansFromTail(repo, harness, path string, base int64, data []byte) []spanRow {
	out := []spanRow{}
	offset := 0
	for offset <= len(data) {
		rest := data[offset:]
		i := bytes.IndexByte(rest, '\n')
		var line []byte
		next := len(data)
		if i >= 0 {
			line = rest[:i]
			next = offset + i + 1
		} else {
			line = rest
		}
		text := strings.TrimSpace(string(line))
		if decisionShaped(text) {
			title := text
			if len(title) > 180 {
				title = title[:180]
			}
			out = append(out, spanRow{
				Repository: repo,
				Harness:    harness,
				Path:       path,
				ByteStart:  base + int64(offset),
				ByteEnd:    base + int64(next),
				Signature:  extractProblemSignature(text),
				Title:      title,
			})
		}
		if i < 0 {
			break
		}
		offset = next
	}
	return out
}

func readSpanBytes(path string, start, end int64) ([]byte, error) {
	spanReadCount++
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if err := spanBoundsOK(info.Size(), start, end); err != nil {
		return nil, err
	}
	buf := make([]byte, end-start)
	_, err = f.ReadAt(buf, start)
	return buf, err
}

func spanWatermarkKey(repository, path string) string {
	return "span_wm:" + repository + ":" + path
}

func parseWatermark(v string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func transcriptScanEnabled() bool {
	if strings.TrimSpace(os.Getenv("OVERDRIVE_TRANSCRIPT_ROOT")) != "" {
		return true
	}
	return os.Getenv("OVERDRIVE_TRANSCRIPT_SCAN") == "1"
}

func transcriptScanRoots() []string {
	if v := strings.TrimSpace(os.Getenv("OVERDRIVE_TRANSCRIPT_ROOT")); v != "" {
		return []string{v}
	}
	if os.Getenv("OVERDRIVE_TRANSCRIPT_SCAN") != "1" {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{
		filepath.Join(home, ".claude"),
		filepath.Join(home, ".codex"),
		filepath.Join(home, ".cursor"),
		filepath.Join(home, ".copilot"),
		filepath.Join(home, ".gemini"),
		filepath.Join(home, ".kiro"),
		filepath.Join(home, ".grok"),
		filepath.Join(home, ".kimi-code"),
		filepath.Join(home, ".config", "opencode"),
	}
}

func pathMatchesRepo(path, repository string) bool {
	base := repository
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	base = strings.TrimSuffix(base, ".git")
	base = strings.TrimSpace(base)
	if base == "" {
		return false
	}
	return strings.Contains(strings.ToLower(path), strings.ToLower(base))
}

func (e *Engine) indexTranscriptFile(project Project, harness, path string) (bool, error) {
	if e == nil || e.db == nil {
		return false, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	key := spanWatermarkKey(project.Repository, path)
	old := parseWatermark(e.getMeta(key))
	size := info.Size()
	if size <= old {
		return false, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	buf := make([]byte, size-old)
	if _, err := f.ReadAt(buf, old); err != nil {
		return false, err
	}
	for _, row := range spansFromTail(project.Repository, harness, path, old, buf) {
		if _, err := e.db.Exec(`INSERT INTO transcript_spans(repository, harness, path, byte_start, byte_end, signature, title, confirmed)
			VALUES (?, ?, ?, ?, ?, ?, ?, 0)`,
			row.Repository, row.Harness, row.Path, row.ByteStart, row.ByteEnd, row.Signature, row.Title); err != nil {
			return false, err
		}
	}
	e.setMeta(key, strconv.FormatInt(size, 10))
	return true, nil
}

func (e *Engine) indexHarnessTranscriptsUnderRoot(project Project, root string) (bool, error) {
	moved := false
	n := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(path), ".jsonl") {
			return nil
		}
		if !pathMatchesRepo(path, project.Repository) {
			return nil
		}
		n++
		if n > 8 {
			return fs.SkipAll
		}
		grew, walkErr := e.indexTranscriptFile(project, "harness", path)
		if grew {
			moved = true
		}
		return walkErr
	})
	if err != nil && !errors.Is(err, fs.SkipAll) {
		return moved, err
	}
	return moved, nil
}

func (e *Engine) indexHarnessTails(project Project) (bool, error) {
	if !transcriptScanEnabled() {
		return false, nil
	}
	moved := false
	for _, root := range transcriptScanRoots() {
		grew, err := e.indexHarnessTranscriptsUnderRoot(project, root)
		if err != nil {
			return moved, err
		}
		if grew {
			moved = true
		}
	}
	return moved, nil
}

func (e *Engine) unconfirmedTitles(project Project) ([]string, error) {
	if e == nil || e.db == nil {
		return nil, nil
	}
	rows, err := e.db.Query(`SELECT title FROM transcript_spans WHERE repository = ? AND confirmed = 0 ORDER BY id DESC LIMIT 6`, project.Repository)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			return nil, err
		}
		if strings.TrimSpace(title) != "" {
			out = append(out, title)
		}
	}
	return out, rows.Err()
}

func (e *Engine) matchingSpanHints(project Project, query string) ([]string, error) {
	if e == nil || e.db == nil || strings.TrimSpace(query) == "" {
		return nil, nil
	}
	rows, err := e.db.Query(`SELECT path, byte_start, byte_end, signature, title FROM transcript_spans
		WHERE repository = ? AND confirmed = 0 ORDER BY id DESC LIMIT 20`, project.Repository)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var row spanRow
		if err := rows.Scan(&row.Path, &row.ByteStart, &row.ByteEnd, &row.Signature, &row.Title); err != nil {
			return nil, err
		}
		if !spanMatches(row, query) {
			continue
		}
		body, err := readSpanBytes(row.Path, row.ByteStart, row.ByteEnd)
		if err != nil {
			continue
		}
		text := strings.TrimSpace(string(body))
		if text != "" {
			out = append(out, text)
		}
	}
	return out, rows.Err()
}

func spanMatches(row spanRow, query string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return false
	}
	blob := strings.ToLower(row.Signature + " " + row.Title)
	if strings.Contains(blob, q) {
		return true
	}
	for _, tok := range strings.Fields(q) {
		if len(tok) >= 4 && strings.Contains(blob, tok) {
			return true
		}
	}
	return false
}
