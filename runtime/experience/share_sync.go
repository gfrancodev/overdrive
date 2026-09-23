package main

import ()

func (e *Engine) syncCirclePeers(project Project) error {
	circle, ok := circleForProject(e.home, project.Root)
	if !ok {
		return nil
	}
	id, priv, err := loadOrCreateIdentity(e.home)
	if err != nil {
		return nil
	}
	if !syncCirclePreconditions(circle, project.Root, id.DeviceID) {
		return nil
	}

	if shareListenEnabled() {
		ensureShareListener(e.home)
	}

	report := shareSyncReport{}
	since := e.getMeta(shareCursorMetaKey(circle.ID, project.Repository))
	for _, endpoint := range nonEmptyEndpoints(circle.PeerEndpoints) {
		result := sharePeerSyncResult{Contacted: true}
		resp, err := fetchPeerSync(e.home, endpoint, circle, id, priv, project.Repository, since)
		if err != nil {
			applySharePeerSyncResult(&report, result, nil)
			continue
		}
		result.OK = true
		imported, err := e.importPeerBatch(circle, resp)
		if err != nil {
			applySharePeerSyncResult(&report, result, nil)
			continue
		}
		result.Imported = imported
		result.Cursor = resp.Cursor
		applySharePeerSyncResult(&report, result, func(cursor string) {
			e.setMeta(shareCursorMetaKey(circle.ID, project.Repository), cursor)
		})
	}
	e.recordShareSyncReport(circle.ID, project.Repository, report)
	return nil
}

func (e *Engine) importPeerBatch(circle Circle, resp SyncResponse) (int, error) {
	if err := validatePeerBatchCircle(circle, resp); err != nil {
		return 0, err
	}
	imported := 0
	embedder := embedderName()
	for _, p := range resp.Packets {
		if !peerPacketImportable(p, embedder) {
			continue
		}
		m := sharedPacketToMemory(p, circle.ID, resp.DeviceID)
		if m.ScopeID == "" {
			continue
		}
		existing, err := e.getMemory(m.ID)
		if !peerMemoryShouldUpdate(existing, err, m.EvidenceScore) {
			continue
		}
		if err := e.upsertPeerMemory(m, p.Vector); err != nil {
			return imported, err
		}
		imported++
	}
	return imported, nil
}

func memberPubKeyFor(c Circle, deviceID string) string {
	for _, m := range c.Members {
		if m.DeviceID == deviceID {
			return m.PublicKey
		}
	}
	return ""
}

func (e *Engine) upsertPeerMemory(m Memory, vector []float32) error {
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
		confidence=excluded.confidence, evidence_score=excluded.evidence_score, updated_at=excluded.updated_at,
		packet_content=excluded.packet_content, problem_signature=excluded.problem_signature,
		status=excluded.status, hot_index=excluded.hot_index`,
		m.ID, vectorID, m.Kind, m.Scope, m.ScopeID, m.Subject, m.Content, m.Confidence, m.Priority, m.Status,
		m.Source, m.SourceRef, m.Evidence, m.SuccessCount, m.FailureCount, m.EvidenceScore,
		m.CreatedAt, m.UpdatedAt, m.LastValidatedAt,
		m.Origin, m.PeerDeviceID, m.CircleID, m.Layer, m.PacketContent, m.ProblemSignature, m.SourceFolder, m.VectorSpace, boolToInt(m.HotIndex),
	)
	if err != nil {
		return err
	}
	if e.tvPeer != nil && e.tvPeer.Available() && m.Status == "active" && m.HotIndex {
		if len(vector) == e.tvPeer.dim && m.VectorSpace == embedderName() {
			e.tvPeer.Add(m.ID, vector)
		} else {
			text := m.PacketContent
			if text == "" {
				text = buildPacketContent(m)
			}
			e.tvPeer.Add(m.ID, embedText(text))
		}
		e.tvPeer.Sync()
	}
	return nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (e *Engine) markPeerRevoked(circleID, deviceID string) error {
	_, err := e.db.Exec(`UPDATE memories SET status = 'deprecated', hot_index = 0 WHERE origin = 'peer' AND circle_id = ? AND peer_device_id = ?`,
		circleID, deviceID)
	if err != nil {
		return err
	}
	if e.tvPeer != nil && e.tvPeer.Available() {
		rows, err := e.db.Query(`SELECT id FROM memories WHERE origin = 'peer' AND circle_id = ? AND peer_device_id = ?`, circleID, deviceID)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var id string
				if rows.Scan(&id) == nil {
					e.tvPeer.Remove(id)
				}
			}
			e.tvPeer.Sync()
		}
	}
	return nil
}
