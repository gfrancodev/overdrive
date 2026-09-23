package main

import (
	"sort"
	"strings"
)

type recallPools struct {
	local map[string]Memory
	peer  map[string]Memory
}

func partitionRecallMemories(memories []Memory, project Project, layer string, peerOK func(Memory) bool) recallPools {
	out := recallPools{local: map[string]Memory{}, peer: map[string]Memory{}}
	for _, m := range memories {
		if m.Status != "active" || !matchesLayer(m.Kind, layer) || !m.HotIndex {
			continue
		}
		if m.Origin == "peer" {
			if peerOK != nil && peerOK(m) {
				out.peer[m.ID] = m
			}
			continue
		}
		if scopeWeight(m, project) == 0 {
			continue
		}
		out.local[m.ID] = m
	}
	return out
}

func recallLocalAllowIDs(ftsIDs []string, localPool map[string]Memory) []uint64 {
	allow := make([]uint64, 0, len(localPool))
	for _, id := range ftsIDs {
		if _, ok := localPool[id]; ok {
			allow = append(allow, memoryVectorID(id))
		}
	}
	if len(allow) == 0 {
		for id := range localPool {
			allow = append(allow, memoryVectorID(id))
		}
	}
	return allow
}

func mapVectorScoresToIDs(tvIDs []uint64, tvScores []float32, pool map[string]Memory) map[string]float64 {
	scores := map[string]float64{}
	for i, vid := range tvIDs {
		for id := range pool {
			if memoryVectorID(id) == vid {
				scores[id] = normalizeVectorScore(tvScores[i])
				break
			}
		}
	}
	return scores
}

func criticalRecallScore(m Memory, project Project) float64 {
	return 10 + scopeWeight(m, project) + m.Confidence + float64(m.Priority)/100
}

func shouldIncludeLocalRecall(m Memory, q string, ftsRanks map[string]float64, vs float64, floor float64) bool {
	if q != "" && queryRelevance(m, q) < 0.08 {
		if _, ok := ftsRanks[m.ID]; !ok {
			return false
		}
	}
	if vs > 0 && vs < floor {
		if _, ok := ftsRanks[m.ID]; !ok {
			return false
		}
	}
	return true
}

func ftsBoostedVectorScore(memoryID string, vs float64, ftsRanks map[string]float64) float64 {
	if r, ok := ftsRanks[memoryID]; ok && vs == 0 {
		return clamp(r/10.0, 0, 1)
	}
	return vs
}

func buildLocalRecallResults(localPool map[string]Memory, q string, ftsRanks map[string]float64, vectorScores map[string]float64, project Project, floor float64) (regular, critical []Memory) {
	regular = []Memory{}
	critical = []Memory{}
	for _, m := range localPool {
		if isCritical(m) {
			c := m
			c.Score = criticalRecallScore(m, project)
			critical = append(critical, c)
			continue
		}
		vs := vectorScores[m.ID]
		if !shouldIncludeLocalRecall(m, q, ftsRanks, vs, floor) {
			continue
		}
		vs = ftsBoostedVectorScore(m.ID, vs, ftsRanks)
		score := recallScore(m, q, scopeWeight(m, project), vs)
		c := m
		c.Score = round(score, 6)
		c.VectorScore = vs
		regular = append(regular, c)
	}
	return regular, critical
}

func shouldIncludePeerRecall(m Memory, querySig, q string, vs float64, floor float64, tvPeerAvailable bool, embedder string) bool {
	if tvPeerAvailable && q != "" && vs < floor {
		return false
	}
	if !concreteSignatureOverlap(querySig, m.ProblemSignature) {
		return false
	}
	if m.VectorSpace != "" && m.VectorSpace != embedder {
		return false
	}
	return true
}

func buildPeerRecallResults(peerPool map[string]Memory, q, querySig string, vectorScores map[string]float64, floor float64, tvPeerAvailable bool, embedder string) []Memory {
	out := []Memory{}
	for _, m := range peerPool {
		vs := vectorScores[m.ID]
		if !shouldIncludePeerRecall(m, querySig, q, vs, floor, tvPeerAvailable, embedder) {
			continue
		}
		score := peerRecallScore(m, q, vs)
		c := m
		c.Score = round(score, 6)
		c.VectorScore = vs
		if c.PacketContent != "" {
			c.Content = c.PacketContent
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > maxPeerRecallItems {
		out = out[:maxPeerRecallItems]
	}
	return out
}

func normalizeRecallLimit(limit int) int {
	if limit < 1 {
		return 1
	}
	return limit
}

func capRecallResults(regular, critical []Memory, limit int) ([]Memory, []Memory) {
	sort.SliceStable(critical, func(i, j int) bool { return critical[i].Score > critical[j].Score })
	sort.SliceStable(regular, func(i, j int) bool { return regular[i].Score > regular[j].Score })
	limit = normalizeRecallLimit(limit)
	if len(regular) > limit {
		regular = regular[:limit]
	}
	if len(critical) > 8 {
		critical = critical[:8]
	}
	return regular, critical
}

func recallSeedCandidates(critical, regular []Memory, max int) []Memory {
	if max <= 0 {
		return nil
	}
	combined := append([]Memory{}, critical...)
	combined = append(combined, regular...)
	if len(combined) > max {
		combined = combined[:max]
	}
	return combined
}

func peerRecallVectorAllow(peerPool map[string]Memory) []uint64 {
	allow := make([]uint64, 0, len(peerPool))
	for id := range peerPool {
		allow = append(allow, memoryVectorID(id))
	}
	return allow
}

func recallQueryTrimmed(query string) string {
	return strings.TrimSpace(query)
}
