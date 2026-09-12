package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(api[_-]?key|access[_-]?token|refresh[_-]?token|secret|password|passwd|authorization)\s*[:=]\s*([^\s,;]+)`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`),
	regexp.MustCompile(`(?i)\b(sk_live|sk_test|ghp|github_pat|hf)_[A-Za-z0-9_-]{8,}\b`),
}

func writeJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, "overdrive-runtime:", message)
	os.Exit(2)
}

func clamp(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func round(v float64, decimals int) float64 {
	p := math.Pow(10, float64(decimals))
	return math.Round(v*p) / p
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func memoryFingerprint(scope, scopeID, kind, subject, content string) string {
	normalized := strings.Join([]string{scope, scopeID, kind, normalize(subject), normalize(content)}, "\x00")
	sum := sha256.Sum256([]byte(normalized))
	return "mem_" + hex.EncodeToString(sum[:10])
}

func memoryVectorID(id string) uint64 {
	sum := sha256.Sum256([]byte(id))
	return binary.LittleEndian.Uint64(sum[:8])
}

func normalize(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(s))), " ")
}

func redact(s string) string {
	if s == "" {
		return ""
	}
	out := s
	for i, re := range secretPatterns {
		if i == 0 {
			out = re.ReplaceAllString(out, "$1=<redacted>")
		} else {
			out = re.ReplaceAllString(out, "<redacted>")
		}
	}
	if strings.Contains(out, "-----BEGIN") && strings.Contains(out, "PRIVATE KEY-----") {
		return "<redacted-private-key>"
	}
	return out
}

func overdriveHome() (string, error) {
	if v := strings.TrimSpace(os.Getenv("OVERDRIVE_HOME")); v != "" {
		return filepath.Abs(v)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".overdrive"), nil
}

func dbPath(home string) string {
	return filepath.Join(home, "experience-v2.db")
}

func vectorIndexPath(home string) string {
	return filepath.Join(home, "experience-v2.tvim")
}

func libDir(home string) string {
	return filepath.Join(home, "lib")
}

func normalizeMemoryScope(m *Memory) {
	if m == nil || strings.TrimSpace(m.ScopeID) != "" {
		return
	}
	switch m.Scope {
	case "global":
		m.ScopeID = "*"
	}
}

func ensureHome() (string, error) {
	home, err := overdriveHome()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", err
	}
	if err := os.MkdirAll(libDir(home), 0o700); err != nil {
		return "", err
	}
	return home, nil
}

func validKind(k string) bool {
	switch k {
	case "fact", "rule", "decision", "preference", "procedure", "lesson", "anti_pattern", "episode":
		return true
	default:
		return false
	}
}

func validScope(s string) bool {
	return s == "global" || s == "organization" || s == "repository" || s == "module"
}

func positiveSource(source string) bool {
	return source == "verified_execution" || source == "verification" || source == "user_feedback" || source == "project_instruction" || source == "adr"
}

func strongestSource(a, b string) string {
	if sourceRank(b) > sourceRank(a) {
		return b
	}
	return a
}

func sourceRank(source string) int {
	return int(sourceTrust(source) * 100)
}

func matchesLayer(kind, layer string) bool {
	switch strings.ToLower(strings.TrimSpace(layer)) {
	case "", "all":
		return true
	case "knowledge":
		switch kind {
		case "fact", "rule", "decision", "preference", "procedure":
			return true
		default:
			return false
		}
	case "lessons":
		return kind == "lesson" || kind == "anti_pattern"
	case "episodes":
		return kind == "episode"
	default:
		return true
	}
}

func parseIntEnv(name string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
