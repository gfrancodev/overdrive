package main

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

func identifyProject(cwd string) (Project, error) {
	abs, err := hookFilepathAbs(cwd)
	if err != nil {
		return Project{}, err
	}
	root := strings.TrimSpace(runGit(abs, "rev-parse", "--show-toplevel"))
	if root == "" {
		root = abs
	}
	root, _ = hookFilepathAbs(root)

	remote := strings.TrimSpace(runGit(root, "config", "--get", "remote.origin.url"))
	repo, org := normalizeRemote(remote)
	if repo == "" {
		canonical := filepath.ToSlash(root)
		sum := sha256.Sum256([]byte(canonical))
		repo = "local/" + filepath.Base(root) + "-" + hex.EncodeToString(sum[:4])
		org = "local"
	}

	module := ""
	if rel, err := filepath.Rel(root, abs); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		module = filepath.ToSlash(rel)
	}
	return Project{Root: root, Repository: repo, Organization: org, Module: module}, nil
}

func normalizeRemote(remote string) (string, string) {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return "", ""
	}
	remote = strings.TrimSuffix(remote, ".git")
	var host, path string

	if strings.Contains(remote, "://") {
		rest := remote[strings.Index(remote, "://")+3:]
		slash := strings.Index(rest, "/")
		if slash < 0 {
			return "", ""
		}
		authority := rest[:slash]
		if at := strings.LastIndex(authority, "@"); at >= 0 {
			authority = authority[at+1:]
		}
		host = authority
		path = rest[slash+1:]
	} else if at := strings.Index(remote, "@"); at >= 0 && strings.Contains(remote[at:], ":") {
		rest := remote[at+1:]
		colon := strings.Index(rest, ":")
		host = rest[:colon]
		path = rest[colon+1:]
	} else {
		return "", ""
	}

	path = strings.Trim(strings.TrimSuffix(path, ".git"), "/")
	if host == "" || path == "" {
		return "", ""
	}
	repo := strings.ToLower(host) + "/" + path
	parts := strings.Split(path, "/")
	org := strings.ToLower(host)
	if len(parts) > 1 {
		org += "/" + parts[0]
	}
	return repo, org
}

func runGit(cwd string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	cmd.Stderr = nil
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func scopeIDFor(scope string, p Project) string {
	switch scope {
	case "global":
		return "*"
	case "organization":
		return p.Organization
	case "repository":
		return p.Repository
	case "module":
		if p.Module == "" {
			return p.Repository + "#root"
		}
		return p.Repository + "#" + p.Module
	default:
		return ""
	}
}

func scopeWeight(m Memory, p Project) float64 {
	switch m.Scope {
	case "global":
		if m.ScopeID == "*" {
			return 0.72
		}
	case "organization":
		if m.ScopeID == p.Organization {
			return 0.84
		}
	case "repository":
		if m.ScopeID == p.Repository {
			return 0.96
		}
	case "module":
		if m.ScopeID == scopeIDFor("module", p) {
			return 1.0
		}
		prefix := p.Repository + "#"
		if strings.HasPrefix(m.ScopeID, prefix) && p.Module != "" {
			storedModule := strings.TrimPrefix(m.ScopeID, prefix)
			if storedModule != "root" && (p.Module == storedModule || strings.HasPrefix(p.Module, storedModule+"/")) {
				return 0.98
			}
		}
	}
	return 0
}

func queryRelevance(m Memory, query string) float64 {
	text := strings.Join([]string{m.Subject, m.Content, m.Evidence}, " ")
	lexical := tokenCosine(query, text)
	hashed := hashedCosine(query, text)
	if hashed < 0 {
		hashed = 0
	}
	return 0.68*lexical + 0.32*hashed
}

func recallScore(m Memory, query string, scopeWeight float64, vectorScore float64) float64 {
	text := strings.Join([]string{m.Subject, m.Content, m.Evidence}, " ")
	lexical := tokenCosine(query, text)
	hashed := hashedCosine(query, text)
	confidence := clamp(m.Confidence, 0, 1)
	priority := float64(clampInt(m.Priority, 0, 100)) / 100
	trust := sourceTrust(m.Source)
	freshness := freshnessScore(m.UpdatedAt)
	evidence := m.EvidenceScore
	if evidence == 0 {
		evidence = evidenceScore(m)
	}

	if strings.TrimSpace(query) == "" {
		lexical, hashed = 0.5, 0.5
	}

	if vectorScore <= 0 {
		return 0.24*lexical + 0.18*hashed + 0.17*scopeWeight + 0.11*confidence + 0.07*trust + 0.05*freshness + 0.04*priority + 0.04*evidence + 0.10*0
	}
	return 0.18*lexical + 0.12*hashed + 0.20*vectorScore + 0.15*scopeWeight + 0.10*confidence + 0.07*trust + 0.05*freshness + 0.04*priority + 0.04*evidence + 0.05*0
}

func tokenize(s string) []string {
	var b strings.Builder
	var prev rune
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if unicode.IsUpper(r) && prev != 0 && (unicode.IsLower(prev) || unicode.IsDigit(prev)) {
				b.WriteByte(' ')
			}
			b.WriteRune(unicode.ToLower(r))
			prev = r
			continue
		}
		b.WriteByte(' ')
		prev = 0
	}
	raw := strings.Fields(b.String())
	out := make([]string, 0, len(raw))
	for _, t := range raw {
		if len(t) < 2 || stopword(t) {
			continue
		}
		out = append(out, t)
	}
	return out
}

var stopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "from": true, "this": true, "that": true,
	"use": true, "using": true, "uma": true, "um": true, "de": true, "do": true, "da": true,
	"dos": true, "das": true, "e": true, "em": true, "para": true, "com": true, "por": true,
	"no": true, "na": true,
}

func stopword(s string) bool {
	return stopwords[s]
}

func tokenCosine(a, b string) float64 {
	av := termFreq(tokenize(a))
	bv := termFreq(tokenize(b))
	if len(av) == 0 || len(bv) == 0 {
		return 0
	}
	var dot, an, bn float64
	for k, v := range av {
		dot += v * bv[k]
		an += v * v
	}
	for _, v := range bv {
		bn += v * v
	}
	if an == 0 || bn == 0 {
		return 0
	}
	return dot / (math.Sqrt(an) * math.Sqrt(bn))
}

func termFreq(tokens []string) map[string]float64 {
	out := map[string]float64{}
	for _, t := range tokens {
		out[t]++
	}
	return out
}

func hashedCosine(a, b string) float64 {
	if hookHashedCosine != nil {
		return hookHashedCosine(a, b)
	}
	av := hashedVector(a)
	bv := hashedVector(b)
	var dot, an, bn float64
	for i := 0; i < vectorDims; i++ {
		dot += av[i] * bv[i]
		an += av[i] * av[i]
		bn += bv[i] * bv[i]
	}
	if an == 0 || bn == 0 {
		return 0
	}
	return dot / (math.Sqrt(an) * math.Sqrt(bn))
}

func hashedVector(s string) [vectorDims]float64 {
	var v [vectorDims]float64
	tokens := tokenize(s)
	features := make([]string, 0, len(tokens)*6)
	features = append(features, tokens...)
	for _, token := range tokens {
		runes := []rune(token)
		if len(runes) >= 4 {
			for i := 0; i+3 <= len(runes); i++ {
				features = append(features, "tri:"+string(runes[i:i+3]))
			}
		}
	}
	for i := 0; i+1 < len(tokens); i++ {
		features = append(features, tokens[i]+"::"+tokens[i+1])
	}
	for _, f := range features {
		h := sha256.Sum256([]byte(f))
		idx := int(uint16(h[0])<<8|uint16(h[1])) % vectorDims
		sign := 1.0
		if h[2]&1 == 1 {
			sign = -1
		}
		v[idx] += sign
	}
	return v
}

func hashedVectorFloat32(s string) []float32 {
	hv := hashedVector(s)
	out := make([]float32, vectorDims)
	for i, v := range hv {
		out[i] = float32(v)
	}
	return out
}

func freshnessScore(ts string) float64 {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return 0.5
	}
	days := time.Since(t).Hours() / 24
	if days < 0 {
		days = 0
	}
	return math.Exp(-days / 1300)
}

func evidenceScore(m Memory) float64 {
	count := m.SuccessCount + m.FailureCount
	score := 0.35
	if strings.TrimSpace(m.Evidence) != "" {
		score += 0.25
	}
	score += math.Min(float64(count)*0.08, 0.4)
	return clamp(score, 0, 1)
}

func isCritical(m Memory) bool {
	if m.Kind != "rule" && m.Kind != "anti_pattern" {
		return false
	}
	if m.Priority >= 90 && m.Confidence >= 0.85 {
		return true
	}
	if (m.Source == "user_feedback" || m.Source == "project_instruction" || m.Source == "adr") && m.Confidence >= 0.9 {
		return true
	}
	return false
}

var sourceTrustLevels = map[string]float64{
	"user_feedback": 1.0, "project_instruction": 1.0, "adr": 1.0,
	"verified_execution": 0.92, "verification": 0.92, "review": 0.92,
	"repository_observation": 0.82, "tests": 0.82, "debugging": 0.82,
	"agent_observation": 0.65,
	"peer_share": 0.55,
	"external": 0.45, "web": 0.45, "issue_description": 0.45,
}

func sourceTrust(source string) float64 {
	if trust, ok := sourceTrustLevels[source]; ok {
		return trust
	}
	return 0.6
}

func peerRecallScore(m Memory, query string, vectorScore float64) float64 {
	text := strings.Join([]string{m.PacketContent, m.Subject, m.Content}, " ")
	lexical := tokenCosine(query, text)
	return 0.10*lexical + 0.12*vectorScore + 0.08*m.EvidenceScore + 0.05*sourceTrust(m.Source)
}
