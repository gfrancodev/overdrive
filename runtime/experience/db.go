package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Engine struct {
	home      string
	db        *sql.DB
	tv        *TurboVecIndex
	sessionID string
}

func openEngine() (*Engine, error) {
	home, err := ensureHome()
	if err != nil {
		return nil, err
	}
	installBundledTurboVecLib(home)
	if err := migrateJSONIfNeeded(home); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dbPath(home)+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	e := &Engine{home: home, db: db}
	if err := e.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	_ = e.db.QueryRow(`SELECT value FROM meta WHERE key = 'current_session_id'`).Scan(&e.sessionID)
	currentEmbedder()
	if err := e.ensureVectorIndex(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return e, nil
}

func (e *Engine) Close() {
	if e.tv != nil {
		e.tv.Sync()
		e.tv.Close()
	}
	if e.db != nil {
		_ = e.db.Close()
	}
}

func (e *Engine) initSchema() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS memories (
			id TEXT PRIMARY KEY,
			vector_id INTEGER NOT NULL UNIQUE,
			kind TEXT NOT NULL,
			scope TEXT NOT NULL,
			scope_id TEXT NOT NULL,
			subject TEXT NOT NULL DEFAULT '',
			content TEXT NOT NULL,
			confidence REAL NOT NULL,
			priority INTEGER NOT NULL,
			status TEXT NOT NULL,
			source TEXT NOT NULL,
			source_ref TEXT NOT NULL DEFAULT '',
			evidence TEXT NOT NULL DEFAULT '',
			success_count INTEGER NOT NULL DEFAULT 0,
			failure_count INTEGER NOT NULL DEFAULT 0,
			evidence_score REAL NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			last_validated_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
			subject, content, evidence,
			content='memories', content_rowid='vector_id'
		)`,
		`CREATE TRIGGER IF NOT EXISTS memories_ai AFTER INSERT ON memories BEGIN
			INSERT INTO memories_fts(rowid, subject, content, evidence)
			VALUES (new.vector_id, new.subject, new.content, new.evidence);
		END`,
		`CREATE TRIGGER IF NOT EXISTS memories_ad AFTER DELETE ON memories BEGIN
			INSERT INTO memories_fts(memories_fts, rowid, subject, content, evidence)
			VALUES ('delete', old.vector_id, old.subject, old.content, old.evidence);
		END`,
		`CREATE TRIGGER IF NOT EXISTS memories_au AFTER UPDATE ON memories BEGIN
			INSERT INTO memories_fts(memories_fts, rowid, subject, content, evidence)
			VALUES ('delete', old.vector_id, old.subject, old.content, old.evidence);
			INSERT INTO memories_fts(rowid, subject, content, evidence)
			VALUES (new.vector_id, new.subject, new.content, new.evidence);
		END`,
		`CREATE TABLE IF NOT EXISTS conflicts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			memory_id TEXT NOT NULL,
			winner_note TEXT NOT NULL DEFAULT '',
			replaces_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS working_memory (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			repository TEXT NOT NULL,
			kind TEXT NOT NULL,
			subject TEXT NOT NULL DEFAULT '',
			content TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS ledger_entries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id TEXT NOT NULL,
			repository TEXT NOT NULL,
			decision TEXT NOT NULL,
			evidence TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL DEFAULT '',
			risk TEXT NOT NULL DEFAULT '',
			reversibility TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			session_id TEXT PRIMARY KEY,
			repository TEXT NOT NULL,
			started_at TEXT NOT NULL
		)`,
	}
	for _, s := range stmts {
		if _, err := e.db.Exec(s); err != nil {
			return err
		}
	}
	_, _ = e.db.Exec(`INSERT OR IGNORE INTO meta(key, value) VALUES ('schema', ?)`, schemaVersion)
	return nil
}

func (e *Engine) memoryCount() (int, error) {
	var n int
	err := e.db.QueryRow(`SELECT COUNT(*) FROM memories`).Scan(&n)
	return n, err
}

func (e *Engine) scanMemory(row scanner) (Memory, error) {
	var m Memory
	var vectorID int64
	err := row.Scan(
		&m.ID, &vectorID, &m.Kind, &m.Scope, &m.ScopeID, &m.Subject, &m.Content,
		&m.Confidence, &m.Priority, &m.Status, &m.Source, &m.SourceRef, &m.Evidence,
		&m.SuccessCount, &m.FailureCount, &m.EvidenceScore, &m.CreatedAt, &m.UpdatedAt, &m.LastValidatedAt,
	)
	return m, err
}

type scanner interface {
	Scan(dest ...any) error
}

const memorySelect = `SELECT id, vector_id, kind, scope, scope_id, subject, content, confidence, priority, status,
	source, source_ref, evidence, success_count, failure_count, evidence_score, created_at, updated_at, last_validated_at
	FROM memories`

func (e *Engine) getMemory(id string) (Memory, error) {
	row := e.db.QueryRow(memorySelect+` WHERE id = ?`, id)
	return e.scanMemory(row)
}

func (e *Engine) listActiveMemories() ([]Memory, error) {
	rows, err := e.db.Query(memorySelect + ` WHERE status = 'active'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Memory{}
	for rows.Next() {
		m, err := e.scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (e *Engine) upsertMemory(m Memory) error {
	normalizeMemoryScope(&m)
	vectorID := int64(memoryVectorID(m.ID))
	if m.EvidenceScore == 0 {
		m.EvidenceScore = evidenceScore(m)
	}
	_, err := e.db.Exec(`INSERT INTO memories(
		id, vector_id, kind, scope, scope_id, subject, content, confidence, priority, status,
		source, source_ref, evidence, success_count, failure_count, evidence_score,
		created_at, updated_at, last_validated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		confidence=excluded.confidence, priority=excluded.priority, status=excluded.status,
		source=excluded.source, source_ref=excluded.source_ref, evidence=excluded.evidence,
		success_count=excluded.success_count, failure_count=excluded.failure_count,
		evidence_score=excluded.evidence_score, updated_at=excluded.updated_at,
		last_validated_at=excluded.last_validated_at`,
		m.ID, vectorID, m.Kind, m.Scope, m.ScopeID, m.Subject, m.Content, m.Confidence, m.Priority, m.Status,
		m.Source, m.SourceRef, m.Evidence, m.SuccessCount, m.FailureCount, m.EvidenceScore,
		m.CreatedAt, m.UpdatedAt, m.LastValidatedAt,
	)
	if err != nil {
		return err
	}
	text := strings.Join([]string{m.Subject, m.Content, m.Evidence}, " ")
	vec := embedText(text)
	if e.tv != nil && e.tv.Available() && m.Status == "active" {
		e.tv.Add(m.ID, vec)
		e.tv.Sync()
	}
	return nil
}

func (e *Engine) deleteMemory(id string) error {
	if e.tv != nil && e.tv.Available() {
		e.tv.Remove(id)
	}
	_, err := e.db.Exec(`DELETE FROM memories WHERE id = ?`, id)
	if err == nil && e.tv != nil && e.tv.Available() {
		e.tv.Sync()
	}
	return err
}

func (e *Engine) ftsCandidates(query string, project Project, limit int) ([]string, map[string]float64, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil, nil
	}
	q := strings.TrimSpace(query)
	rows, err := e.db.Query(`
		SELECT m.id, bm25(memories_fts) AS rank
		FROM memories_fts
		JOIN memories m ON m.vector_id = memories_fts.rowid
		WHERE memories_fts MATCH ? AND m.status = 'active'
		ORDER BY rank
		LIMIT ?
	`, q, limit*4)
	if err != nil {
		// fallback token OR query
		tokens := tokenize(q)
		if len(tokens) == 0 {
			return nil, nil, nil
		}
		parts := make([]string, 0, len(tokens))
		for _, t := range tokens {
			parts = append(parts, t+"*")
		}
		orQ := strings.Join(parts, " OR ")
		rows, err = e.db.Query(`
			SELECT m.id, bm25(memories_fts) AS rank
			FROM memories_fts
			JOIN memories m ON m.vector_id = memories_fts.rowid
			WHERE memories_fts MATCH ? AND m.status = 'active'
			ORDER BY rank
			LIMIT ?
		`, orQ, limit*4)
		if err != nil {
			return nil, nil, nil
		}
	}
	defer rows.Close()
	ids := []string{}
	ranks := map[string]float64{}
	for rows.Next() {
		var id string
		var rank float64
		if err := rows.Scan(&id, &rank); err != nil {
			return nil, nil, err
		}
		m, err := e.getMemory(id)
		if err != nil {
			continue
		}
		if scopeWeight(m, project) == 0 {
			continue
		}
		ids = append(ids, id)
		ranks[id] = -rank
	}
	return ids, ranks, rows.Err()
}

func migrateJSONIfNeeded(home string) error {
	dbFile := dbPath(home)
	if _, err := os.Stat(dbFile); err == nil {
		return nil
	}
	jsonFile := filepath.Join(home, "experience-v1.json")
	data, err := os.ReadFile(jsonFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var store JSONStore
	if err := json.Unmarshal(data, &store); err != nil {
		return fmt.Errorf("migrate json: %w", err)
	}
	e, err := openEngineWithoutMigrate(home)
	if err != nil {
		return err
	}
	defer e.Close()
	for _, m := range store.Memories {
		if m.Status == "" {
			m.Status = "active"
		}
		normalizeMemoryScope(&m)
		if err := e.upsertMemory(m); err != nil {
			return err
		}
	}
	return nil
}

func openEngineWithoutMigrate(home string) (*Engine, error) {
	db, err := sql.Open("sqlite", dbPath(home)+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	e := &Engine{home: home, db: db}
	if err := e.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	_ = e.db.QueryRow(`SELECT value FROM meta WHERE key = 'current_session_id'`).Scan(&e.sessionID)
	currentEmbedder()
	if err := e.ensureVectorIndex(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return e, nil
}

func (e *Engine) getMeta(key string) string {
	var value string
	_ = e.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&value)
	return value
}

func (e *Engine) setMeta(key, value string) {
	_, _ = e.db.Exec(`INSERT OR REPLACE INTO meta(key, value) VALUES (?, ?)`, key, value)
}

func (e *Engine) ensureVectorIndex() error {
	dim := embedDim()
	storedDim := parseInt(e.getMeta("embedder_dim"), -1)
	needRebuild := storedDim >= 0 && storedDim != dim

	if e.tv != nil {
		e.tv.Close()
		e.tv = nil
	}
	if needRebuild {
		_ = os.Remove(vectorIndexPath(e.home))
	}

	e.tv = openTurboVec(e.home, dim)
	if e.tv == nil || !e.tv.Available() {
		_ = os.Remove(vectorIndexPath(e.home))
		e.tv = openTurboVec(e.home, dim)
		needRebuild = true
	}

	e.setMeta("embedder_dim", strconv.Itoa(dim))
	if needRebuild || storedDim < 0 {
		return e.reindexActiveMemories()
	}
	return nil
}

func (e *Engine) reindexActiveMemories() error {
	if e.tv == nil || !e.tv.Available() {
		return nil
	}
	memories, err := e.listActiveMemories()
	if err != nil {
		return err
	}
	for _, m := range memories {
		text := strings.Join([]string{m.Subject, m.Content, m.Evidence}, " ")
		e.tv.Add(m.ID, embedText(text))
	}
	e.tv.Sync()
	return nil
}

func (e *Engine) runGC(previousSessionID string) error {
	now := time.Now().UTC()
	cutoffDeprecated := now.AddDate(0, 0, -90).Format(time.RFC3339)
	cutoffStale := now.AddDate(0, 0, -180).Format(time.RFC3339)

	if previousSessionID != "" {
		_, _ = e.db.Exec(`DELETE FROM working_memory WHERE session_id = ?`, previousSessionID)
	}

	rows, err := e.db.Query(`SELECT id, source, status, confidence, updated_at FROM memories WHERE status IN ('deprecated', 'stale')`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, source, status, updatedAt string
		var confidence float64
		if err := rows.Scan(&id, &source, &status, &confidence, &updatedAt); err != nil {
			return err
		}
		protected := source == "user_feedback" || source == "project_instruction" || source == "adr"
		if status == "deprecated" && updatedAt < cutoffDeprecated {
			if err := e.deleteMemory(id); err != nil {
				return err
			}
			continue
		}
		if status == "stale" && confidence < 0.25 && updatedAt < cutoffStale && !protected {
			if err := e.deleteMemory(id); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Engine) startSession(project Project) (string, error) {
	var previous string
	_ = e.db.QueryRow(`SELECT value FROM meta WHERE key = 'current_session_id'`).Scan(&previous)
	sessionID := fmt.Sprintf("sess_%d", time.Now().UTC().UnixNano())
	_, err := e.db.Exec(`INSERT OR REPLACE INTO meta(key, value) VALUES ('current_session_id', ?)`, sessionID)
	if err != nil {
		return "", err
	}
	_, err = e.db.Exec(`INSERT INTO sessions(session_id, repository, started_at) VALUES (?, ?, ?)`,
		sessionID, project.Repository, nowRFC3339())
	if err != nil {
		return "", err
	}
	if err := e.runGC(previous); err != nil {
		return "", err
	}
	e.sessionID = sessionID
	return sessionID, nil
}

func (e *Engine) addWorkingMemory(project Project, kind, subject, content string) error {
	if e.sessionID == "" {
		return nil
	}
	_, err := e.db.Exec(`INSERT INTO working_memory(session_id, repository, kind, subject, content, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, e.sessionID, project.Repository, kind, redact(subject), redact(content), nowRFC3339())
	return err
}

func (e *Engine) listWorkingMemory(project Project) ([]Memory, error) {
	if e.sessionID == "" {
		return nil, nil
	}
	rows, err := e.db.Query(`SELECT kind, subject, content, created_at FROM working_memory
		WHERE session_id = ? AND repository = ? ORDER BY id DESC LIMIT 12`, e.sessionID, project.Repository)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Memory{}
	for rows.Next() {
		var m Memory
		if err := rows.Scan(&m.Kind, &m.Subject, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.Scope = "session"
		m.Status = "active"
		out = append(out, m)
	}
	return out, rows.Err()
}

func (e *Engine) recordMemory(project Project, kind, scope, subject, content string, confidence float64, priority int, source, sourceRef, evidence string) (Memory, error) {
	scopeID := scopeIDFor(scope, project)
	if scopeID == "" {
		return Memory{}, fmt.Errorf("cannot resolve scope %q for current project", scope)
	}
	now := nowRFC3339()
	cleanContent := redact(strings.TrimSpace(content))
	cleanEvidence := redact(strings.TrimSpace(evidence))
	cleanSubject := redact(strings.TrimSpace(subject))
	conf := clamp(confidence, 0, 1)
	if source == "user_feedback" || source == "project_instruction" || source == "adr" {
		conf = math.Max(conf, 0.95)
	}
	fp := memoryFingerprint(scope, scopeID, kind, cleanSubject, cleanContent)

	existing, err := e.getMemory(fp)
	if err == nil {
		existing.Confidence = clamp(math.Max(existing.Confidence, conf)+(1-math.Max(existing.Confidence, conf))*0.02, 0, 1)
		existing.Priority = maxInt(existing.Priority, clampInt(priority, 0, 100))
		existing.UpdatedAt = now
		existing.Status = "active"
		existing.Source = strongestSource(existing.Source, source)
		if cleanEvidence != "" {
			existing.Evidence = cleanEvidence
		}
		if sourceRef != "" {
			existing.SourceRef = redact(sourceRef)
		}
		if positiveSource(source) {
			existing.SuccessCount++
			existing.LastValidatedAt = now
		}
		existing.EvidenceScore = evidenceScore(existing)
		if err := e.upsertMemory(existing); err != nil {
			return Memory{}, err
		}
		return existing, nil
	}

	m := Memory{
		ID: fp, Kind: kind, Scope: scope, ScopeID: scopeID, Subject: cleanSubject, Content: cleanContent,
		Confidence: conf, Priority: clampInt(priority, 0, 100), Status: "active", Source: source,
		SourceRef: redact(strings.TrimSpace(sourceRef)), Evidence: cleanEvidence,
		CreatedAt: now, UpdatedAt: now, EvidenceScore: 0,
	}
	if positiveSource(source) {
		m.SuccessCount = 1
		m.LastValidatedAt = now
	}
	m.EvidenceScore = evidenceScore(m)
	if err := e.upsertMemory(m); err != nil {
		return Memory{}, err
	}
	return m, nil
}

func (e *Engine) recall(project Project, query string, limit int, layer string) (RecallResponse, error) {
	memories, err := e.listActiveMemories()
	if err != nil {
		return RecallResponse{}, err
	}
	q := strings.TrimSpace(query)
	ftsIDs, ftsRanks, _ := e.ftsCandidates(q, project, limit)

	allowlist := make([]uint64, 0, len(ftsIDs))
	idToMem := map[string]Memory{}
	vectorScores := map[string]float64{}
	for _, m := range memories {
		if !matchesLayer(m.Kind, layer) {
			continue
		}
		if scopeWeight(m, project) == 0 {
			continue
		}
		idToMem[m.ID] = m
	}
	for _, id := range ftsIDs {
		if _, ok := idToMem[id]; ok {
			allowlist = append(allowlist, memoryVectorID(id))
		}
	}
	if len(allowlist) == 0 {
		for id := range idToMem {
			allowlist = append(allowlist, memoryVectorID(id))
		}
	}

	if e.tv != nil && e.tv.Available() && q != "" {
		vec := embedText(q)
		tvIDs, tvScores := e.tv.Search(vec, limit*2, allowlist)
		for i, vid := range tvIDs {
			for id := range idToMem {
				if memoryVectorID(id) == vid {
					vectorScores[id] = normalizeVectorScore(tvScores[i])
					break
				}
			}
		}
	}

	regular := []Memory{}
	critical := []Memory{}
	for _, m := range memories {
		if m.Status != "active" || !matchesLayer(m.Kind, layer) {
			continue
		}
		sw := scopeWeight(m, project)
		if sw == 0 {
			continue
		}
		if isCritical(m) {
			c := m
			c.Score = 10 + sw + m.Confidence + float64(m.Priority)/100
			critical = append(critical, c)
			continue
		}
		if q != "" {
			if queryRelevance(m, q) < 0.08 {
				if _, ok := ftsRanks[m.ID]; !ok {
					continue
				}
			}
		}
		vs := vectorScores[m.ID]
		if r, ok := ftsRanks[m.ID]; ok && vs == 0 {
			vs = clamp(r/10.0, 0, 1)
		}
		score := recallScore(m, q, sw, vs)
		c := m
		c.Score = round(score, 6)
		regular = append(regular, c)
	}

	sort.SliceStable(critical, func(i, j int) bool { return critical[i].Score > critical[j].Score })
	sort.SliceStable(regular, func(i, j int) bool { return regular[i].Score > regular[j].Score })
	if limit < 1 {
		limit = 1
	}
	if len(regular) > limit {
		regular = regular[:limit]
	}
	if len(critical) > 8 {
		critical = critical[:8]
	}
	if e.sessionID != "" && q != "" {
		seeded := 0
		for _, m := range append(append([]Memory{}, critical...), regular...) {
			if seeded >= 6 {
				break
			}
			_ = e.addWorkingMemory(project, m.Kind, m.Subject, m.Content)
			seeded++
		}
	}
	wm, _ := e.listWorkingMemory(project)
	if wm == nil {
		wm = []Memory{}
	}
	return RecallResponse{
		Project:       project,
		Memories:      regular,
		CriticalRules: critical,
		WorkingMemory: wm,
		Backend:       backendName,
	}, nil
}

func (e *Engine) validateMemory(id, result, winnerNote, replacesID string) (Memory, error) {
	m, err := e.getMemory(id)
	if err != nil {
		return Memory{}, fmt.Errorf("memory %q not found", id)
	}
	now := nowRFC3339()
	switch result {
	case "success":
		m.SuccessCount++
		m.Confidence = clamp(m.Confidence+(1-m.Confidence)*0.05, 0, 1)
		m.Status = "active"
	case "failure":
		m.FailureCount++
		m.Confidence = clamp(m.Confidence*0.85, 0, 1)
		if m.Confidence < 0.35 {
			m.Status = "stale"
		}
	case "contradiction":
		m.FailureCount++
		m.Confidence = clamp(m.Confidence*0.5, 0, 1)
		m.Status = "deprecated"
		if e.tv != nil && e.tv.Available() {
			e.tv.Remove(id)
			e.tv.Sync()
		}
		_, _ = e.db.Exec(`INSERT INTO conflicts(memory_id, winner_note, replaces_id, created_at) VALUES (?, ?, ?, ?)`,
			id, redact(winnerNote), strings.TrimSpace(replacesID), now)
	default:
		return Memory{}, errors.New("--result must be success, failure, or contradiction")
	}
	m.UpdatedAt = now
	m.LastValidatedAt = now
	m.EvidenceScore = evidenceScore(m)
	if err := e.upsertMemory(m); err != nil {
		return Memory{}, err
	}
	return m, nil
}

func (e *Engine) ledgerAdd(project Project, runID, decision, evidence, reason, risk, reversibility string) (LedgerEntry, error) {
	res, err := e.db.Exec(`INSERT INTO ledger_entries(run_id, repository, decision, evidence, reason, risk, reversibility, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		runID, project.Repository, redact(decision), redact(evidence), redact(reason), redact(risk), redact(reversibility), nowRFC3339())
	if err != nil {
		return LedgerEntry{}, err
	}
	id, _ := res.LastInsertId()
	entry := LedgerEntry{
		ID: id, RunID: runID, Decision: decision, Evidence: evidence, Reason: reason,
		Risk: risk, Reversibility: reversibility, CreatedAt: nowRFC3339(),
	}
	entries, err := e.ledgerList(project, runID)
	if err != nil {
		return entry, err
	}
	if err := writeLedgerMarkdown(e.home, project, runID, entries); err != nil {
		return entry, err
	}
	return entry, nil
}

func (e *Engine) ledgerList(project Project, runID string) ([]LedgerEntry, error) {
	rows, err := e.db.Query(`SELECT id, run_id, decision, evidence, reason, risk, reversibility, created_at
		FROM ledger_entries WHERE run_id = ? AND repository = ? ORDER BY id ASC`, runID, project.Repository)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LedgerEntry{}
	for rows.Next() {
		var e LedgerEntry
		if err := rows.Scan(&e.ID, &e.RunID, &e.Decision, &e.Evidence, &e.Reason, &e.Risk, &e.Reversibility, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func writeLedgerMarkdown(home string, project Project, runID string, entries []LedgerEntry) error {
	dir := filepath.Join(home, "runs", sanitizeRunID(runID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# Decision Ledger\n\n")
	for _, entry := range entries {
		b.WriteString(fmt.Sprintf("## R-%d\n\n", entry.ID))
		b.WriteString(entry.Decision + "\n\n")
		if entry.Evidence != "" {
			b.WriteString("Evidence: " + entry.Evidence + "\n\n")
		}
		if entry.Reason != "" {
			b.WriteString("Reason: " + entry.Reason + "\n\n")
		}
		if entry.Risk != "" {
			b.WriteString("Risk if wrong: " + entry.Risk + "\n\n")
		}
		if entry.Reversibility != "" {
			b.WriteString("Reversibility: " + entry.Reversibility + "\n\n")
		}
	}
	return os.WriteFile(filepath.Join(dir, "ledger.md"), []byte(b.String()), 0o600)
}

func sanitizeRunID(runID string) string {
	runID = strings.TrimSpace(runID)
	runID = strings.ReplaceAll(runID, "..", "")
	runID = strings.ReplaceAll(runID, "/", "-")
	if runID == "" {
		return "default"
	}
	return runID
}

func withEngine(fn func(*Engine) error) error {
	e, err := openEngine()
	if err != nil {
		return err
	}
	defer e.Close()
	return fn(e)
}

func parseFloat(s string, fallback float64) float64 {
	if s == "" {
		return fallback
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fallback
	}
	return v
}

func parseInt(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return v
}
