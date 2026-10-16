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

	// Register the modernc SQLite driver for database/sql.
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
			created_at TEXT NOT NULL,
			lifetime TEXT NOT NULL DEFAULT 'run'
		)`,
		`CREATE TABLE IF NOT EXISTS handoffs (
			id TEXT PRIMARY KEY,
			repository TEXT NOT NULL,
			summary TEXT NOT NULL,
			state TEXT NOT NULL,
			owner_device TEXT NOT NULL DEFAULT '',
			claimed_by TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS transcript_spans (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			repository TEXT NOT NULL,
			harness TEXT NOT NULL,
			path TEXT NOT NULL,
			byte_start INTEGER NOT NULL,
			byte_end INTEGER NOT NULL,
			signature TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL DEFAULT '',
			confirmed INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			session_id TEXT PRIMARY KEY,
			repository TEXT NOT NULL,
			started_at TEXT NOT NULL
		)`,
	}
	stmts = append(stmts, graphSchemaStmts()...)
	for _, s := range stmts {
		if _, err := e.db.Exec(s); err != nil {
			return err
		}
	}
	_, _ = e.db.Exec(`INSERT OR IGNORE INTO meta(key, value) VALUES ('schema', ?)`, schemaVersion)
	return e.migrateSchema()
}

const sqlTextNotNullEmpty = "TEXT NOT NULL DEFAULT ''"

func memoryMigrationColumns() []string {
	return []string{
		"origin", "peer_device_id", "circle_id", "layer",
		"packet_content", "problem_signature", "source_folder", "vector_space", "hot_index",
	}
}

func memoryColumnDefinition(col string) (string, bool) {
	switch col {
	case "origin":
		return "TEXT NOT NULL DEFAULT 'local'", true
	case "peer_device_id", "circle_id", "packet_content", "problem_signature", "source_folder", "vector_space":
		return sqlTextNotNullEmpty, true
	case "layer":
		return "INTEGER NOT NULL DEFAULT 2", true
	case "hot_index":
		return "INTEGER NOT NULL DEFAULT 1", true
	default:
		return "", false
	}
}

func alterMemoryColumnSQL(col string) (string, bool) {
	switch col {
	case "origin":
		return `ALTER TABLE memories ADD COLUMN origin TEXT NOT NULL DEFAULT 'local'`, true
	case "peer_device_id":
		return `ALTER TABLE memories ADD COLUMN peer_device_id TEXT NOT NULL DEFAULT ''`, true
	case "circle_id":
		return `ALTER TABLE memories ADD COLUMN circle_id TEXT NOT NULL DEFAULT ''`, true
	case "layer":
		return `ALTER TABLE memories ADD COLUMN layer INTEGER NOT NULL DEFAULT 2`, true
	case "packet_content":
		return `ALTER TABLE memories ADD COLUMN packet_content TEXT NOT NULL DEFAULT ''`, true
	case "problem_signature":
		return `ALTER TABLE memories ADD COLUMN problem_signature TEXT NOT NULL DEFAULT ''`, true
	case "source_folder":
		return `ALTER TABLE memories ADD COLUMN source_folder TEXT NOT NULL DEFAULT ''`, true
	case "vector_space":
		return `ALTER TABLE memories ADD COLUMN vector_space TEXT NOT NULL DEFAULT ''`, true
	case "hot_index":
		return `ALTER TABLE memories ADD COLUMN hot_index INTEGER NOT NULL DEFAULT 1`, true
	default:
		return "", false
	}
}

func (e *Engine) migrateSchema() error {
	for _, col := range memoryMigrationColumns() {
		if err := e.ensureMemoryColumn(col); err != nil {
			return err
		}
	}
	if err := e.ensureLedgerLifetimeColumn(); err != nil {
		return err
	}
	_, _ = e.db.Exec(`UPDATE meta SET value = ? WHERE key = 'schema'`, schemaVersion)
	e.backfillGraph()
	return nil
}

func (e *Engine) ensureMemoryColumn(col string) error {
	if columnExists(e.db, "memories", col) {
		return nil
	}
	def, ok := memoryColumnDefinition(col)
	if !ok {
		return fmt.Errorf("unknown memory column %s", col)
	}
	if hookMigrateAddColumn != nil {
		return hookMigrateAddColumn(e.db, col, def)
	}
	stmt, ok := alterMemoryColumnSQL(col)
	if !ok {
		return fmt.Errorf("unknown memory column %s", col)
	}
	_, err := e.db.Exec(stmt)
	return err
}

func (e *Engine) ensureLedgerLifetimeColumn() error {
	if columnExists(e.db, "ledger_entries", "lifetime") {
		return nil
	}
	_, err := e.db.Exec(`ALTER TABLE ledger_entries ADD COLUMN lifetime TEXT NOT NULL DEFAULT 'run'`)
	return err
}

func columnExists(db *sql.DB, table, col string) bool {
	if db == nil {
		return false
	}
	rows, err := tableInfoQuery(db, table)
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

func tableInfoQuery(db *sql.DB, table string) (*sql.Rows, error) {
	switch table {
	case "memories":
		return db.Query(`PRAGMA table_info(memories)`)
	case "ledger_entries":
		return db.Query(`PRAGMA table_info(ledger_entries)`)
	default:
		return nil, fmt.Errorf("unsupported table %s", table)
	}
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

const memorySelectByID = `SELECT id, vector_id, kind, scope, scope_id, subject, content, confidence, priority, status,
	source, source_ref, evidence, success_count, failure_count, evidence_score, created_at, updated_at, last_validated_at,
	origin, peer_device_id, circle_id, layer, packet_content, problem_signature, source_folder, vector_space, hot_index
	FROM memories WHERE id = ?`

const memorySelectActive = `SELECT id, vector_id, kind, scope, scope_id, subject, content, confidence, priority, status,
	source, source_ref, evidence, success_count, failure_count, evidence_score, created_at, updated_at, last_validated_at,
	origin, peer_device_id, circle_id, layer, packet_content, problem_signature, source_folder, vector_space, hot_index
	FROM memories WHERE status = 'active'`

const memorySelectExportable = `SELECT id, vector_id, kind, scope, scope_id, subject, content, confidence, priority, status,
	source, source_ref, evidence, success_count, failure_count, evidence_score, created_at, updated_at, last_validated_at,
	origin, peer_device_id, circle_id, layer, packet_content, problem_signature, source_folder, vector_space, hot_index
	FROM memories WHERE status = 'active' AND origin = 'local' AND scope = 'repository' AND scope_id = ?`

func (e *Engine) getMemory(id string) (Memory, error) {
	row := e.db.QueryRow(memorySelectByID, id)
	return e.scanMemory(row)
}

func (e *Engine) listActiveMemories() ([]Memory, error) {
	rows, err := e.db.Query(memorySelectActive)
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

func defaultSQLCommit(tx *sql.Tx) error { return tx.Commit() }

var hookSQLCommit = defaultSQLCommit

func (e *Engine) persistMemory(m Memory, extra []GraphEdge) error {
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
	if e.db == nil {
		return errGraphStoreUnavailable
	}
	tx, err := e.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO memories(
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
	); err != nil {
		return err
	}
	if err := syncMemoryGraphOn(tx, m, extra); err != nil {
		return err
	}
	if err := hookSQLCommit(tx); err != nil {
		return err
	}
	e.indexPersistedMemory(m)
	return nil
}

func memoryIndexText(m Memory) string {
	if m.PacketContent != "" {
		return m.PacketContent
	}
	return strings.Join([]string{m.Subject, m.Content, m.Evidence}, " ")
}

func (e *Engine) indexPersistedMemory(m Memory) {
	vec := embedText(memoryIndexText(m))
	if m.Origin == "peer" {
		e.addPeerVector(m, vec)
		return
	}
	e.addLocalVector(m, vec)
}

func (e *Engine) addPeerVector(m Memory, vec []float32) {
	if e.tvPeer == nil || !e.tvPeer.Available() || m.Status != "active" || !m.HotIndex {
		return
	}
	e.tvPeer.Add(m.ID, vec)
	e.tvPeer.Sync()
}

func (e *Engine) addLocalVector(m Memory, vec []float32) {
	if e.tv == nil || !e.tv.Available() || m.Status != "active" || !m.HotIndex {
		return
	}
	e.tv.Add(m.ID, vec)
	e.tv.Sync()
}

func (e *Engine) upsertMemory(m Memory) error {
	return e.persistMemory(m, nil)
}

func (e *Engine) deleteMemory(id string) error {
	if hookDeleteMemory != nil {
		return hookDeleteMemory(e, id)
	}
	if e == nil || e.db == nil {
		return errGraphStoreUnavailable
	}
	m, getErr := e.getMemory(id)
	tx, err := e.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM memories WHERE id = ?`, id); err != nil {
		return err
	}
	if err := removeMemoryGraphOn(tx, id); err != nil {
		return err
	}
	if err := hookSQLCommit(tx); err != nil {
		return err
	}
	e.removeMemoryVector(id, m, getErr)
	return nil
}

func (e *Engine) removeMemoryVector(id string, m Memory, getErr error) {
	if getErr == nil {
		e.removeLoadedMemoryVector(id, m)
		return
	}
	if e.tv != nil && e.tv.Available() {
		e.tv.Remove(id)
		e.tv.Sync()
	}
}

func (e *Engine) removeLoadedMemoryVector(id string, m Memory) {
	if m.Origin == "peer" && e.tvPeer != nil && e.tvPeer.Available() {
		e.tvPeer.Remove(id)
		e.tvPeer.Sync()
		return
	}
	if e.tv != nil && e.tv.Available() {
		e.tv.Remove(id)
		e.tv.Sync()
	}
}

func (e *Engine) ftsCandidates(query string, project Project, limit int) ([]string, map[string]float64, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil, nil
	}
	q := strings.TrimSpace(query)
	rows, err := e.queryFTS(q, limit*4)
	if err != nil {
		rows, err = e.queryFTSFallback(q, limit*4)
		if err != nil {
			return nil, nil, nil
		}
	}
	return e.collectFTSHits(rows, project)
}

func (e *Engine) queryFTS(match string, limit int) (*sql.Rows, error) {
	return e.db.Query(`
		SELECT m.id, bm25(memories_fts) AS rank
		FROM memories_fts
		JOIN memories m ON m.vector_id = memories_fts.rowid
		WHERE memories_fts MATCH ? AND m.status = 'active'
		ORDER BY rank
		LIMIT ?
	`, match, limit)
}

func (e *Engine) queryFTSFallback(query string, limit int) (*sql.Rows, error) {
	tokens := tokenize(query)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("no fts tokens")
	}
	parts := make([]string, 0, len(tokens))
	for _, t := range tokens {
		parts = append(parts, t+"*")
	}
	return e.queryFTS(strings.Join(parts, " OR "), limit)
}

func (e *Engine) collectFTSHits(rows *sql.Rows, project Project) ([]string, map[string]float64, error) {
	defer rows.Close()
	ids := []string{}
	ranks := map[string]float64{}
	for rows.Next() {
		id, rank, err := scanFTSHit(rows)
		if err != nil {
			return nil, nil, err
		}
		if !e.ftsHitInScope(id, project) {
			continue
		}
		ids = append(ids, id)
		ranks[id] = -rank
	}
	return ids, ranks, rows.Err()
}

func scanFTSHit(rows *sql.Rows) (string, float64, error) {
	var id string
	var rank float64
	if hookFTSScanErr != nil {
		return "", 0, hookFTSScanErr
	}
	err := rows.Scan(&id, &rank)
	return id, rank, err
}

func (e *Engine) ftsHitInScope(id string, project Project) bool {
	m, err := e.getMemory(id)
	if err != nil {
		return false
	}
	return scopeWeight(m, project) != 0
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
		e.reindexOneMemory(m)
	}
	e.syncVectorIndexes()
	return nil
}

func (e *Engine) reindexOneMemory(m Memory) {
	if !m.HotIndex {
		return
	}
	vec := embedText(memoryIndexText(m))
	if m.Origin == "peer" {
		if e.tvPeer != nil && e.tvPeer.Available() {
			e.tvPeer.Add(m.ID, vec)
		}
		return
	}
	if e.tv != nil && e.tv.Available() {
		e.tv.Add(m.ID, vec)
	}
}

func (e *Engine) syncVectorIndexes() {
	if e.tv != nil && e.tv.Available() {
		e.tv.Sync()
	}
	if e.tvPeer != nil && e.tvPeer.Available() {
		e.tvPeer.Sync()
	}
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
		if err := e.deleteScannedGCRow(rows, cutoffDeprecated, cutoffStale); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) deleteScannedGCRow(rows *sql.Rows, cutoffDeprecated, cutoffStale string) error {
	id, source, status, updatedAt, confidence, err := scanGCRow(rows)
	if err != nil {
		return err
	}
	return e.deleteGCCandidate(id, source, status, updatedAt, confidence, cutoffDeprecated, cutoffStale)
}

func scanGCRow(rows *sql.Rows) (string, string, string, string, float64, error) {
	var id, source, status, updatedAt string
	var confidence float64
	if hookRunGCScanErr != nil {
		return "", "", "", "", 0, hookRunGCScanErr
	}
	err := rows.Scan(&id, &source, &status, &confidence, &updatedAt)
	return id, source, status, updatedAt, confidence, err
}

func (e *Engine) deleteGCCandidate(id, source, status, updatedAt string, confidence float64, cutoffDeprecated, cutoffStale string) error {
	if status == "deprecated" && shouldDeleteDeprecatedMemory(updatedAt, cutoffDeprecated) {
		return e.deleteMemory(id)
	}
	if status == "stale" && shouldDeleteStaleMemory(source, updatedAt, cutoffStale, confidence) {
		return e.deleteMemory(id)
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
	e.importPeerCatalogs(project)
	if transcriptScanEnabled() {
		if moved, err := e.indexHarnessTails(project); err == nil && moved {
			_ = e.ensureHandoffFromHints(project)
		}
	}
	e.sessionID = sessionID
	return sessionID, nil
}

func (e *Engine) endSession(project Project) error {
	if err := e.promoteStandingDecisions(project); err != nil {
		return err
	}
	if err := e.captureSessionHandoff(project); err != nil {
		return err
	}
	circle, ok := circleForProject(e.home, project.Root)
	if ok && folderAllowed(circle, project.Root) {
		if err := e.consolidateSession(project, circle); err != nil {
			return err
		}
	}
	if shareListenEnabled() {
		_ = e.syncCirclePeers(project)
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
		if hookRowsScanErr != nil {
			err = hookRowsScanErr
		} else {
			err = rows.Scan(&m.Kind, &m.Subject, &m.Content, &m.CreatedAt)
		}
		if err != nil {
			return nil, err
		}
		m.Scope = "session"
		m.Status = "active"
		out = append(out, m)
	}
	return out, rows.Err()
}

type memoryRecord struct {
	Project    Project
	Kind       string
	Scope      string
	Subject    string
	Content    string
	Confidence float64
	Priority   int
	Source     string
	SourceRef  string
	Evidence   string
}

type preparedMemoryRecord struct {
	in          memoryRecord
	scopeID     string
	now         string
	subject     string
	content     string
	evidence    string
	confidence  float64
	fingerprint string
}

func trustedRecordSource(source string) bool {
	return source == "user_feedback" || source == "project_instruction" || source == "adr"
}

func prepareMemoryRecord(in memoryRecord) (preparedMemoryRecord, error) {
	scopeID := scopeIDFor(in.Scope, in.Project)
	if scopeID == "" {
		return preparedMemoryRecord{}, fmt.Errorf("cannot resolve scope %q for current project", in.Scope)
	}
	conf := clamp(in.Confidence, 0, 1)
	if trustedRecordSource(in.Source) {
		conf = math.Max(conf, 0.95)
	}
	subject := redact(strings.TrimSpace(in.Subject))
	content := redact(strings.TrimSpace(in.Content))
	return preparedMemoryRecord{
		in: in, scopeID: scopeID, now: nowRFC3339(), subject: subject, content: content,
		evidence: redact(strings.TrimSpace(in.Evidence)), confidence: conf,
		fingerprint: memoryFingerprint(in.Scope, scopeID, in.Kind, subject, content),
	}, nil
}

func (e *Engine) recordMemory(in memoryRecord) (Memory, error) {
	if hookRecordMemory != nil {
		return hookRecordMemory(e, in)
	}
	prepared, err := prepareMemoryRecord(in)
	if err != nil {
		return Memory{}, err
	}
	existing, err := e.getMemory(prepared.fingerprint)
	if err == nil {
		return e.mergeRecordedMemory(existing, prepared)
	}
	return e.insertRecordedMemory(prepared)
}

func (e *Engine) mergeRecordedMemory(existing Memory, prepared preparedMemoryRecord) (Memory, error) {
	conf := prepared.confidence
	existing.Confidence = clamp(math.Max(existing.Confidence, conf)+(1-math.Max(existing.Confidence, conf))*0.02, 0, 1)
	existing.Priority = maxInt(existing.Priority, clampInt(prepared.in.Priority, 0, 100))
	existing.UpdatedAt = prepared.now
	existing.Status = "active"
	existing.Source = strongestSource(existing.Source, prepared.in.Source)
	if prepared.evidence != "" {
		existing.Evidence = prepared.evidence
	}
	if prepared.in.SourceRef != "" {
		existing.SourceRef = redact(prepared.in.SourceRef)
	}
	if positiveSource(prepared.in.Source) {
		existing.SuccessCount++
		existing.LastValidatedAt = prepared.now
	}
	applyDistillation(&existing, prepared.in.Project)
	existing.EvidenceScore = evidenceScore(existing)
	if err := e.upsertMemory(existing); err != nil {
		return Memory{}, err
	}
	e.maybePush(prepared.in.Project, existing)
	return existing, nil
}

func (e *Engine) insertRecordedMemory(prepared preparedMemoryRecord) (Memory, error) {
	m := Memory{
		ID: prepared.fingerprint, Kind: prepared.in.Kind, Scope: prepared.in.Scope, ScopeID: prepared.scopeID,
		Subject: prepared.subject, Content: prepared.content,
		Confidence: prepared.confidence, Priority: clampInt(prepared.in.Priority, 0, 100), Status: "active",
		Source: prepared.in.Source, SourceRef: redact(strings.TrimSpace(prepared.in.SourceRef)), Evidence: prepared.evidence,
		CreatedAt: prepared.now, UpdatedAt: prepared.now, EvidenceScore: 0, Origin: "local", Layer: 2, HotIndex: true,
	}
	if positiveSource(prepared.in.Source) {
		m.SuccessCount = 1
		m.LastValidatedAt = prepared.now
	}
	applyDistillation(&m, prepared.in.Project)
	m.EvidenceScore = evidenceScore(m)
	if err := e.upsertMemory(m); err != nil {
		return Memory{}, err
	}
	e.maybePush(prepared.in.Project, m)
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
	ftsIDs, ftsRanks, _ := e.ftsCandidates(q, project, limit)
	pools := partitionRecallMemories(memories, project, layer, func(m Memory) bool {
		return e.peerEligible(m, project)
	})
	floor := vectorScoreFloor()
	localScores := e.localRecallVectorScores(q, limit, recallLocalAllowIDs(ftsIDs, pools.local), pools.local)
	regular, critical := buildLocalRecallResults(pools.local, q, ftsRanks, localScores, project, floor)
	peerMemories := e.recallPeerMemories(pools, q, extractProblemSignature(q), project, floor)
	regular, critical = capRecallResults(regular, critical, limit)
	e.seedRecallWorkingMemory(project, q, critical, regular)
	wm := e.recallWorkingMemory(project)
	hitIDs, seenHits := recallHitIDs(regular, critical, peerMemories)
	graphNeighbors, pageIndex, graphEdges := e.recallGraph(project, q, hitIDs, seenHits)
	return RecallResponse{
		Project:        project,
		Memories:       regular,
		PeerMemories:   peerMemories,
		CriticalRules:  critical,
		WorkingMemory:  wm,
		GraphNeighbors: graphNeighbors,
		PageIndex:      pageIndex,
		GraphEdges:     graphEdges,
		Unconfirmed:    e.recallUnconfirmed(project, q),
		Backend:        backendName,
	}, nil
}

func (e *Engine) localRecallVectorScores(q string, limit int, localAllow []uint64, pool map[string]Memory) map[string]float64 {
	if e.tv == nil || !e.tv.Available() || q == "" || len(localAllow) == 0 {
		return map[string]float64{}
	}
	tvIDs, tvScores := e.tv.Search(embedText(q), limit*2, localAllow)
	return mapVectorScoresToIDs(tvIDs, tvScores, pool)
}

func (e *Engine) recallPeerMemories(pools recallPools, q, querySig string, project Project, floor float64) []Memory {
	if q == "" || len(pools.peer) == 0 || !e.peerRecallEnabled(project) {
		return []Memory{}
	}
	peerVectorScores := map[string]float64{}
	tvPeerAvailable := e.tvPeer != nil && e.tvPeer.Available()
	if tvPeerAvailable {
		tvIDs, tvScores := e.tvPeer.Search(embedText(q), maxPeerRecallItems*2, peerRecallVectorAllow(pools.peer))
		peerVectorScores = mapVectorScoresToIDs(tvIDs, tvScores, pools.peer)
	}
	return buildPeerRecallResults(pools.peer, q, querySig, peerVectorScores, floor, tvPeerAvailable, embedderName())
}

func (e *Engine) seedRecallWorkingMemory(project Project, q string, critical, regular []Memory) {
	if e.sessionID == "" || q == "" {
		return
	}
	for _, m := range recallSeedCandidates(critical, regular, 6) {
		_ = e.addWorkingMemory(project, m.Kind, m.Subject, m.Content)
	}
}

func (e *Engine) recallWorkingMemory(project Project) []Memory {
	wm, _ := e.listWorkingMemory(project)
	if wm == nil {
		return []Memory{}
	}
	return wm
}

func recallHitIDs(regular, critical, peer []Memory) ([]string, map[string]bool) {
	hitIDs := []string{}
	seenHits := map[string]bool{}
	for _, group := range [][]Memory{regular, critical, peer} {
		for _, m := range group {
			hitIDs = append(hitIDs, m.ID)
			seenHits[m.ID] = true
		}
	}
	return hitIDs, seenHits
}

func (e *Engine) recallGraph(project Project, q string, hitIDs []string, seenHits map[string]bool) ([]Memory, []PageIndexNode, []GraphEdge) {
	graphNeighbors := []Memory{}
	pageIndex := []PageIndexNode{}
	graphEdges := []GraphEdge{}
	if q == "" || len(hitIDs) == 0 {
		return graphNeighbors, pageIndex, graphEdges
	}
	neighborIDs := e.graphNeighborIDs(hitIDs)
	graphNeighbors = e.loadGraphNeighborMemories(neighborIDs, seenHits, project)
	branchIDs := append(append([]string{}, hitIDs...), neighborIDs...)
	pageIndex, graphEdges = e.pageIndexBranch(branchIDs)
	missing := e.unresolvedPageIndexIDs(pageIndex, q)
	e.fetchMissingPeerPackets(project, missing)
	graphNeighbors = append(graphNeighbors, e.loadGraphNeighborMemories(missing, seenHits, project)...)
	pageIndex, graphEdges = e.pageIndexBranch(append(branchIDs, missing...))
	return nonemptyMemories(graphNeighbors), nonemptyPageIndex(pageIndex), nonemptyGraphEdges(graphEdges)
}

func nonemptyMemories(in []Memory) []Memory {
	if in == nil {
		return []Memory{}
	}
	return in
}

func nonemptyPageIndex(in []PageIndexNode) []PageIndexNode {
	if in == nil {
		return []PageIndexNode{}
	}
	return in
}

func nonemptyGraphEdges(in []GraphEdge) []GraphEdge {
	if in == nil {
		return []GraphEdge{}
	}
	return in
}

func (e *Engine) recallUnconfirmed(project Project, q string) []string {
	unconfirmed := []string{}
	if !shouldOpenTranscript(e.listDecisionMemories(project), q) {
		return unconfirmed
	}
	hints, err := e.matchingSpanHints(project, q)
	if err != nil || len(hints) == 0 {
		return unconfirmed
	}
	return hints
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
	var extra []GraphEdge
	if result == "contradiction" && strings.TrimSpace(replacesID) != "" {
		extra = []GraphEdge{{Src: graphMemoryNodeID(replacesID), Dst: graphMemoryNodeID(id), Kind: "supersedes"}}
	}
	if err := e.persistMemory(m, extra); err != nil {
		return Memory{}, err
	}
	return m, nil
}

func (e *Engine) ledgerAdd(project Project, runID, decision, evidence, reason, risk, reversibility string) (LedgerEntry, error) {
	return e.ledgerAddLifetime(ledgerWrite{
		Project: project, RunID: runID, Decision: decision, Evidence: evidence,
		Reason: reason, Risk: risk, Reversibility: reversibility, Lifetime: "run",
	})
}

func (e *Engine) ledgerAddLifetime(in ledgerWrite) (LedgerEntry, error) {
	entry, err := insertLedgerEntryLifetime(e.db, in)
	if err != nil {
		return LedgerEntry{}, err
	}
	entries, err := e.ledgerList(in.Project, in.RunID)
	if err != nil {
		return entry, err
	}
	if err := writeLedgerMarkdown(e.home, in.Project, in.RunID, entries); err != nil {
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
		if hookRowsScanErr != nil {
			err = hookRowsScanErr
		} else {
			err = rows.Scan(&e.ID, &e.RunID, &e.Decision, &e.Evidence, &e.Reason, &e.Risk, &e.Reversibility, &e.CreatedAt)
		}
		if err != nil {
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
