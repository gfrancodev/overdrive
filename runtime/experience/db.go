package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Engine struct {
	home      string
	db        *sql.DB
	tv        *TurboVecIndex
	tvPeer    *TurboVecIndex
	sessionID string
}

func openEngine() (*Engine, error) {
	if hookOpenEngine != nil {
		return hookOpenEngine()
	}
	home, err := ensureHome()
	if err != nil {
		return nil, err
	}
	return newEngine(home)
}

func newEngine(home string) (*Engine, error) {
	if strings.TrimSpace(home) == "" {
		return nil, fmt.Errorf("home is required")
	}
	if err := hookMkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	if err := hookMkdirAll(libDir(home), 0o700); err != nil {
		return nil, err
	}
	installBundledTurboVecLib(home)
	if err := migrateJSONIfNeeded(home); err != nil {
		return nil, err
	}
	db, err := hookDBOpen("sqlite", dbPath(home)+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
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
	if e.tvPeer != nil {
		e.tvPeer.Sync()
		e.tvPeer.Close()
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
	return e.migrateSchema()
}

func (e *Engine) migrateSchema() error {
	cols := map[string]string{
		"origin":            "TEXT NOT NULL DEFAULT 'local'",
		"peer_device_id":    "TEXT NOT NULL DEFAULT ''",
		"circle_id":         "TEXT NOT NULL DEFAULT ''",
		"layer":             "INTEGER NOT NULL DEFAULT 2",
		"packet_content":    "TEXT NOT NULL DEFAULT ''",
		"problem_signature": "TEXT NOT NULL DEFAULT ''",
		"source_folder":     "TEXT NOT NULL DEFAULT ''",
		"vector_space":      "TEXT NOT NULL DEFAULT ''",
		"hot_index":         "INTEGER NOT NULL DEFAULT 1",
	}
	for col, def := range cols {
		if columnExists(e.db, "memories", col) {
			continue
		}
		var err error
		if hookMigrateAddColumn != nil {
			err = hookMigrateAddColumn(e.db, col, def)
		} else {
			_, err = e.db.Exec(`ALTER TABLE memories ADD COLUMN ` + col + ` ` + def)
		}
		if err != nil {
			return err
		}
	}
	_, _ = e.db.Exec(`UPDATE meta SET value = ? WHERE key = 'schema'`, schemaVersion)
	return nil
}

func columnExists(db *sql.DB, table, col string) bool {
	if db == nil {
		return false
	}
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dfltValue sql.NullString
		var pk int
		var err error
		if hookColumnInfoScanErr != nil {
			err = hookColumnInfoScanErr
		} else {
			err = rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk)
		}
		if err != nil {
			return false
		}
		if name == col {
			return true
		}
	}
	return false
}

func (e *Engine) memoryCount() (int, error) {
	var n int
	err := e.db.QueryRow(`SELECT COUNT(*) FROM memories`).Scan(&n)
	return n, err
}

func (e *Engine) scanMemory(row scanner) (Memory, error) {
	if hookScanMemory != nil {
		return hookScanMemory(row)
	}
	var m Memory
	var vectorID int64
	var hot int
	err := row.Scan(
		&m.ID, &vectorID, &m.Kind, &m.Scope, &m.ScopeID, &m.Subject, &m.Content,
		&m.Confidence, &m.Priority, &m.Status, &m.Source, &m.SourceRef, &m.Evidence,
		&m.SuccessCount, &m.FailureCount, &m.EvidenceScore, &m.CreatedAt, &m.UpdatedAt, &m.LastValidatedAt,
		&m.Origin, &m.PeerDeviceID, &m.CircleID, &m.Layer, &m.PacketContent, &m.ProblemSignature,
		&m.SourceFolder, &m.VectorSpace, &hot,
	)
	m.HotIndex = hot == 1
	if m.Origin == "" {
		m.Origin = "local"
	}
	return m, err
}

type scanner interface {
	Scan(dest ...any) error
}

const memorySelect = `SELECT id, vector_id, kind, scope, scope_id, subject, content, confidence, priority, status,
	source, source_ref, evidence, success_count, failure_count, evidence_score, created_at, updated_at, last_validated_at,
	origin, peer_device_id, circle_id, layer, packet_content, problem_signature, source_folder, vector_space, hot_index
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
	if m.Origin == "" {
		m.Origin = "local"
	}
	if m.Origin == "local" && m.Status == "active" && m.Layer == 0 {
		m.Layer = 2
		m.HotIndex = true
	}
	vectorID := int64(memoryVectorID(m.ID))
	if m.EvidenceScore == 0 {
		m.EvidenceScore = evidenceScore(m)
	}
	_, err := e.db.Exec(`INSERT INTO memories(
		id, vector_id, kind, scope, scope_id, subject, content, confidence, priority, status,
		source, source_ref, evidence, success_count, failure_count, evidence_score,
		created_at, updated_at, last_validated_at,
		origin, peer_device_id, circle_id, layer, packet_content, problem_signature, source_folder, vector_space, hot_index
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		confidence=excluded.confidence, priority=excluded.priority, status=excluded.status,
		source=excluded.source, source_ref=excluded.source_ref, evidence=excluded.evidence,
		success_count=excluded.success_count, failure_count=excluded.failure_count,
		evidence_score=excluded.evidence_score, updated_at=excluded.updated_at,
		last_validated_at=excluded.last_validated_at,
		packet_content=excluded.packet_content, problem_signature=excluded.problem_signature,
		source_folder=excluded.source_folder, vector_space=excluded.vector_space, hot_index=excluded.hot_index`,
		m.ID, vectorID, m.Kind, m.Scope, m.ScopeID, m.Subject, m.Content, m.Confidence, m.Priority, m.Status,
		m.Source, m.SourceRef, m.Evidence, m.SuccessCount, m.FailureCount, m.EvidenceScore,
		m.CreatedAt, m.UpdatedAt, m.LastValidatedAt,
		m.Origin, m.PeerDeviceID, m.CircleID, m.Layer, m.PacketContent, m.ProblemSignature, m.SourceFolder, m.VectorSpace, boolToInt(m.HotIndex),
	)
	if err != nil {
		return err
	}
	indexText := m.PacketContent
	if indexText == "" {
		indexText = strings.Join([]string{m.Subject, m.Content, m.Evidence}, " ")
	}
	vec := embedText(indexText)
	if m.Origin == "peer" {
		if e.tvPeer != nil && e.tvPeer.Available() && m.Status == "active" && m.HotIndex {
			e.tvPeer.Add(m.ID, vec)
			e.tvPeer.Sync()
		}
		return nil
	}
	if e.tv != nil && e.tv.Available() && m.Status == "active" && m.HotIndex {
		e.tv.Add(m.ID, vec)
		e.tv.Sync()
	}
	return nil
}

func (e *Engine) deleteMemory(id string) error {
	if hookDeleteMemory != nil {
		return hookDeleteMemory(e, id)
	}
	m, err := e.getMemory(id)
	if err == nil {
		if m.Origin == "peer" && e.tvPeer != nil && e.tvPeer.Available() {
			e.tvPeer.Remove(id)
		} else if e.tv != nil && e.tv.Available() {
			e.tv.Remove(id)
		}
	} else if e.tv != nil && e.tv.Available() {
		e.tv.Remove(id)
	}
	_, err = e.db.Exec(`DELETE FROM memories WHERE id = ?`, id)
	if err == nil {
		if e.tv != nil && e.tv.Available() {
			e.tv.Sync()
		}
		if e.tvPeer != nil && e.tvPeer.Available() {
			e.tvPeer.Sync()
		}
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
		var err error
		if hookFTSScanErr != nil {
			err = hookFTSScanErr
		} else {
			err = rows.Scan(&id, &rank)
		}
		if err != nil {
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
	db, err := hookDBOpen("sqlite", dbPath(home)+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
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
	if hookEnsureVectorIndex != nil {
		return hookEnsureVectorIndex(e)
	}
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
	if e.tvPeer != nil {
		e.tvPeer.Close()
	}
	e.tvPeer = openTurboVecAt(peerVectorIndexPath(e.home), e.home, dim)
	if needRebuild {
		_ = os.Remove(peerVectorIndexPath(e.home))
		e.tvPeer = openTurboVecAt(peerVectorIndexPath(e.home), e.home, dim)
	}

	e.setMeta("embedder_dim", strconv.Itoa(dim))
	if needRebuild || storedDim < 0 {
		return e.reindexActiveMemories()
	}
	return nil
}

func (e *Engine) reindexActiveMemories() error {
	memories, err := e.listActiveMemories()
	if err != nil {
		return err
	}
	for _, m := range memories {
		if !m.HotIndex {
			continue
		}
		text := m.PacketContent
		if text == "" {
			text = strings.Join([]string{m.Subject, m.Content, m.Evidence}, " ")
		}
		vec := embedText(text)
		if m.Origin == "peer" {
			if e.tvPeer != nil && e.tvPeer.Available() {
				e.tvPeer.Add(m.ID, vec)
			}
			continue
		}
		if e.tv != nil && e.tv.Available() {
			e.tv.Add(m.ID, vec)
		}
	}
	if e.tv != nil && e.tv.Available() {
		e.tv.Sync()
	}
	if e.tvPeer != nil && e.tvPeer.Available() {
		e.tvPeer.Sync()
	}
	return nil
}

func (e *Engine) runGC(previousSessionID string) error {
	now := time.Now().UTC()
	cutoffDeprecated := gcDeprecatedCutoff(now)
	cutoffStale := gcStaleCutoff(now)

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
		var err error
		if hookRunGCScanErr != nil {
			err = hookRunGCScanErr
		} else {
			err = rows.Scan(&id, &source, &status, &confidence, &updatedAt)
		}
		if err != nil {
			return err
		}
		if status == "deprecated" && shouldDeleteDeprecatedMemory(updatedAt, cutoffDeprecated) {
			if err := e.deleteMemory(id); err != nil {
				return err
			}
			continue
		}
		if status == "stale" && shouldDeleteStaleMemory(source, updatedAt, cutoffStale, confidence) {
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
	circle, _ := circleForProject(e.home, project.Root)
	if previous != "" {
		_ = e.consolidateSession(project, circle)
	}
	_ = e.syncCirclePeers(project)
	e.sessionID = sessionID
	return sessionID, nil
}

func (e *Engine) endSession(project Project) error {
	circle, ok := circleForProject(e.home, project.Root)
	if ok && folderAllowed(circle, project.Root) {
		if err := e.consolidateSession(project, circle); err != nil {
			return err
		}
	}
	return nil
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
	if hookRecordMemory != nil {
		return hookRecordMemory(e, project, kind, scope, subject, content, confidence, priority, source, sourceRef, evidence)
	}
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
		applyDistillation(&existing, project)
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
		CreatedAt: now, UpdatedAt: now, EvidenceScore: 0, Origin: "local", Layer: 2, HotIndex: true,
	}
	if positiveSource(source) {
		m.SuccessCount = 1
		m.LastValidatedAt = now
	}
	applyDistillation(&m, project)
	m.EvidenceScore = evidenceScore(m)
	if err := e.upsertMemory(m); err != nil {
		return Memory{}, err
	}
	return m, nil
}

func (e *Engine) recall(project Project, query string, limit int, layer string) (RecallResponse, error) {
	if hookEngineRecall != nil {
		return hookEngineRecall(e, project, query, limit, layer)
	}
	memories, err := e.listActiveMemories()
	if err != nil {
		return RecallResponse{}, err
	}
	q := recallQueryTrimmed(query)
	querySig := extractProblemSignature(q)
	ftsIDs, ftsRanks, _ := e.ftsCandidates(q, project, limit)

	pools := partitionRecallMemories(memories, project, layer, func(m Memory) bool {
		return e.peerEligible(m, project)
	})
	localAllow := recallLocalAllowIDs(ftsIDs, pools.local)

	localVectorScores := map[string]float64{}
	if e.tv != nil && e.tv.Available() && q != "" && len(localAllow) > 0 {
		vec := embedText(q)
		tvIDs, tvScores := e.tv.Search(vec, limit*2, localAllow)
		localVectorScores = mapVectorScoresToIDs(tvIDs, tvScores, pools.local)
	}

	floor := vectorScoreFloor()
	regular, critical := buildLocalRecallResults(pools.local, q, ftsRanks, localVectorScores, project, floor)

	peerMemories := []Memory{}
	if q != "" && len(pools.peer) > 0 && e.peerRecallEnabled(project) {
		peerVectorScores := map[string]float64{}
		tvPeerAvailable := e.tvPeer != nil && e.tvPeer.Available()
		if tvPeerAvailable {
			vec := embedText(q)
			tvIDs, tvScores := e.tvPeer.Search(vec, maxPeerRecallItems*2, peerRecallVectorAllow(pools.peer))
			peerVectorScores = mapVectorScoresToIDs(tvIDs, tvScores, pools.peer)
		}
		peerMemories = buildPeerRecallResults(pools.peer, q, querySig, peerVectorScores, floor, tvPeerAvailable, embedderName())
	}

	regular, critical = capRecallResults(regular, critical, limit)
	if e.sessionID != "" && q != "" {
		for _, m := range recallSeedCandidates(critical, regular, 6) {
			_ = e.addWorkingMemory(project, m.Kind, m.Subject, m.Content)
		}
	}
	wm, _ := e.listWorkingMemory(project)
	if wm == nil {
		wm = []Memory{}
	}
	return RecallResponse{
		Project:       project,
		Memories:      regular,
		PeerMemories:  peerMemories,
		CriticalRules: critical,
		WorkingMemory: wm,
		Backend:       backendName,
	}, nil
}

func (e *Engine) peerEligible(m Memory, project Project) bool {
	if m.Status != "active" || m.Origin != "peer" || !m.HotIndex {
		return false
	}
	if m.ScopeID != project.Repository {
		return false
	}
	if m.CircleID == "" {
		return false
	}
	circle, err := loadCircle(e.home, m.CircleID)
	if err != nil {
		return false
	}
	if !folderAllowed(circle, project.Root) {
		return false
	}
	member, ok := circle.activeMember(m.PeerDeviceID)
	return ok && !member.Revoked
}

func (e *Engine) peerRecallEnabled(project Project) bool {
	circle, ok := circleForProject(e.home, project.Root)
	if !ok {
		return false
	}
	return folderAllowed(circle, project.Root)
}

func vectorScoreFloor() float64 {
	if embedderName() == "minilm" {
		return miniLMScoreFloor
	}
	return hashedScoreFloor
}

func applyValidationSuccess(m *Memory) {
	m.SuccessCount++
	m.Confidence = clamp(m.Confidence+(1-m.Confidence)*0.05, 0, 1)
	m.Status = "active"
	m.HotIndex = true
	m.EvidenceScore = clamp(m.EvidenceScore+0.05, 0, 1)
}

func (e *Engine) applyValidationFailure(m *Memory, id string) {
	m.FailureCount++
	m.Confidence = clamp(m.Confidence*0.85, 0, 1)
	if m.Origin == "peer" {
		m.HotIndex = false
		if e.tvPeer != nil && e.tvPeer.Available() {
			e.tvPeer.Remove(id)
			e.tvPeer.Sync()
		}
	}
	if m.Confidence < 0.35 {
		m.Status = "stale"
	}
}

func (e *Engine) applyValidationContradiction(m *Memory, id, winnerNote, replacesID, now string) {
	m.FailureCount++
	m.Confidence = clamp(m.Confidence*0.5, 0, 1)
	m.Status = "deprecated"
	if e.tv != nil && e.tv.Available() {
		e.tv.Remove(id)
		e.tv.Sync()
	}
	_, _ = e.db.Exec(`INSERT INTO conflicts(memory_id, winner_note, replaces_id, created_at) VALUES (?, ?, ?, ?)`,
		id, redact(winnerNote), strings.TrimSpace(replacesID), now)
}

var validationResultHandlers = map[string]func(*Engine, *Memory, string, string, string) error{
	"success": func(e *Engine, m *Memory, _ string, _ string, _ string) error {
		applyValidationSuccess(m)
		return nil
	},
	"failure": func(e *Engine, m *Memory, id, _, _ string) error {
		e.applyValidationFailure(m, id)
		return nil
	},
	"contradiction": func(e *Engine, m *Memory, id, winnerNote, replacesID string) error {
		now := nowRFC3339()
		e.applyValidationContradiction(m, id, winnerNote, replacesID, now)
		return nil
	},
}

func (e *Engine) validateMemory(id, result, winnerNote, replacesID string) (Memory, error) {
	m, err := e.getMemory(id)
	if err != nil {
		return Memory{}, fmt.Errorf("memory %q not found", id)
	}
	now := nowRFC3339()
	handler, ok := validationResultHandlers[result]
	if !ok {
		return Memory{}, errors.New("--result must be success, failure, or contradiction")
	}
	var handlerErr error
	if hookValidateMemoryHandler != nil {
		handlerErr = hookValidateMemoryHandler(e, &m, id, result, winnerNote, replacesID)
	} else {
		handlerErr = handler(e, &m, id, winnerNote, replacesID)
	}
	if handlerErr != nil {
		return Memory{}, handlerErr
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
	entry, err := insertLedgerEntry(e.db, project, runID, decision, evidence, reason, risk, reversibility)
	if err != nil {
		return LedgerEntry{}, err
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
	if hookLedgerList != nil {
		return hookLedgerList(e, project, runID)
	}
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
	if err := hookMkdirAll(dir, 0o700); err != nil {
		return err
	}
	return hookWriteFile(filepath.Join(dir, "ledger.md"), []byte(renderLedgerMarkdown(entries)), 0o600)
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
