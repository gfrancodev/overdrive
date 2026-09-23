package main

import (
	"fmt"
	"os"
	"strings"
)

func shareCursorMetaKey(circleID, repository string) string {
	return "share_cursor:" + circleID + ":" + repository
}

func syncCirclePreconditions(circle Circle, projectRoot, deviceID string) bool {
	if _, ok := circle.activeMember(deviceID); !ok {
		return false
	}
	return folderAllowed(circle, projectRoot)
}

func resolveProjectForExport(project Project, circle Circle) (Project, bool) {
	if project.Root != "" {
		if !folderAllowed(circle, project.Root) {
			return project, false
		}
		return project, true
	}
	for _, folder := range circle.AllowedFolders {
		p, err := identifyProject(folder)
		if err == nil && p.Repository == project.Repository && folderAllowed(circle, p.Root) {
			return p, true
		}
	}
	return project, false
}

func shouldExportMemory(m Memory) bool {
	if !shareableKind(m.Kind) {
		return false
	}
	if m.Scope == "global" || m.Kind == "preference" {
		return false
	}
	return true
}

func memorySyncFolder(m Memory, projectRoot string) string {
	if m.SourceFolder != "" {
		return m.SourceFolder
	}
	return projectRoot
}

func memoryEligibleForSync(m Memory, since, projectRoot string, circle Circle) bool {
	if since != "" && m.UpdatedAt <= since {
		return false
	}
	if !m.HotIndex || m.Layer != 2 {
		return false
	}
	return folderAllowed(circle, memorySyncFolder(m, projectRoot))
}

func buildSyncPackets(memories []Memory, since string, circle Circle, project Project) []SharedPacket {
	packets := []SharedPacket{}
	for _, m := range memories {
		if !memoryEligibleForSync(m, since, project.Root, circle) {
			continue
		}
		folder := memorySyncFolder(m, project.Root)
		packets = append(packets, memoryToSharedPacket(m, folder, m.HotIndex))
	}
	return packets
}

func peerPacketImportable(p SharedPacket, embedder string) bool {
	if !p.HotIndex {
		return false
	}
	if p.VectorSpace != "" && p.VectorSpace != embedder {
		return false
	}
	return true
}

func peerMemoryShouldUpdate(existing Memory, existingErr error, incomingEvidence float64) bool {
	if existingErr != nil {
		return true
	}
	return existing.EvidenceScore < incomingEvidence
}

func validatePeerBatchCircle(circle Circle, resp SyncResponse) error {
	if resp.CircleID != circle.ID {
		return fmt.Errorf("circle mismatch")
	}
	if !circle.isActiveMemberPubKey(memberPubKeyFor(circle, resp.DeviceID)) {
		return fmt.Errorf("revoked peer")
	}
	return nil
}

type sharePeerSyncResult struct {
	Contacted bool
	OK        bool
	Imported  int
	Cursor    string
}

func applySharePeerSyncResult(report *shareSyncReport, result sharePeerSyncResult, setCursor func(string)) {
	if !result.Contacted {
		return
	}
	report.PeersContacted++
	if !result.OK {
		return
	}
	report.PeersOK++
	report.Imported += result.Imported
	if result.Cursor != "" && setCursor != nil {
		setCursor(result.Cursor)
	}
}

func nonEmptyEndpoints(endpoints []string) []string {
	out := []string{}
	for _, ep := range endpoints {
		if strings.TrimSpace(ep) != "" {
			out = append(out, ep)
		}
	}
	return out
}

func shareListenEnabled() bool {
	return strings.TrimSpace(os.Getenv("OVERDRIVE_SHARE_LISTEN")) != ""
}
