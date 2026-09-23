package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func seedRecallBench(t *testing.T, n int) (*Engine, Project) {
	t.Helper()
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "bench-repo"), "https://github.com/acme/bench-search.git")
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	project, err := identifyProject(repo)
	if err != nil {
		t.Fatal(err)
	}
	topics := []string{
		"jwt", "middleware", "postgres", "redis", "cache", "migration", "handler",
		"repository", "validation", "timeout", "retry", "circuit", "peer", "sync",
	}
	for i := 0; i < n; i++ {
		topic := topics[i%len(topics)]
		content := fmt.Sprintf(
			"lesson %d: validate %s tokens in %s layer before exporting peer sync payloads ERR_%04d",
			i, topic, topics[(i+3)%len(topics)], i%1000,
		)
		kind := "lesson"
		if i%17 == 0 {
			kind = "rule"
		}
		if _, err := e.recordMemory(project, kind, "repository", topic, content, 0.6+float64(i%40)/100, 40+i%60, "agent_observation", "", ""); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	return e, project
}

func benchRecallQuery() string {
	return "validate jwt middleware before peer sync timeout"
}

func BenchmarkRecall100(b *testing.B) {
	benchmarkRecall(b, 100)
}

func BenchmarkRecall1000(b *testing.B) {
	benchmarkRecall(b, 1000)
}

func BenchmarkRecall5000(b *testing.B) {
	benchmarkRecall(b, 5000)
}

func benchmarkRecall(b *testing.B, n int) {
	e, project := seedRecallBench(&testing.T{}, n)
	defer e.Close()
	q := benchRecallQuery()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := e.recall(project, q, 12, "")
		if err != nil {
			b.Fatal(err)
		}
		if len(resp.Memories)+len(resp.CriticalRules) == 0 {
			b.Fatal("expected hits")
		}
	}
}

func BenchmarkFTSCandidates1000(b *testing.B) {
	e, project := seedRecallBench(&testing.T{}, 1000)
	defer e.Close()
	q := benchRecallQuery()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ids, _, err := e.ftsCandidates(q, project, 12)
		if err != nil {
			b.Fatal(err)
		}
		if len(ids) == 0 {
			b.Skip("no fts hits for benchmark query")
		}
	}
}

func BenchmarkListActiveMemories5000(b *testing.B) {
	e, _ := seedRecallBench(&testing.T{}, 5000)
	defer e.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mem, err := e.listActiveMemories()
		if err != nil {
			b.Fatal(err)
		}
		if len(mem) != 5000 {
			b.Fatalf("count %d", len(mem))
		}
	}
}

func BenchmarkTurboVecSearch1000(b *testing.B) {
	e, project := seedRecallBench(&testing.T{}, 1000)
	defer e.Close()
	if e.tv == nil || !e.tv.Available() {
		b.Skip("turbovec unavailable")
	}
	memories, err := e.listActiveMemories()
	if err != nil {
		b.Fatal(err)
	}
	pools := partitionRecallMemories(memories, project, "", func(Memory) bool { return false })
	allow := recallLocalAllowIDs(nil, pools.local)
	vec := embedText(benchRecallQuery())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ids, scores := e.tv.Search(vec, 24, allow)
		if len(ids) == 0 || len(scores) == 0 {
			b.Fatal("expected vector hits")
		}
	}
}

func TestRecallBenchReport(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	sizes := []int{100, 1000, 5000}
	queries := []string{
		benchRecallQuery(),
		"postgres migration handler",
		"ERR_0423",
	}
	tv := "off"
	for _, n := range sizes {
		e, project := seedRecallBench(t, n)
		if e.tv != nil && e.tv.Available() {
			tv = "on"
		}
		for _, q := range queries {
			start := time.Now()
			resp, err := e.recall(project, q, 12, "")
			elapsed := time.Since(start)
			if err != nil {
				t.Fatal(err)
			}
			hits := len(resp.Memories) + len(resp.CriticalRules) + len(resp.PeerMemories)
			t.Logf("recall n=%d turbovec=%s q=%q hits=%d latency=%s",
				n, tv, truncBench(q, 40), hits, elapsed)
		}
		e.Close()
	}
}

func truncBench(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
