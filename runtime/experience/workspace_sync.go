package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	sharedWorkspaceManifestFile = "manifest.json"
	sharedWorkspaceMaxBytes     = 64 * 1024 * 1024
)

// SharedWorkspaceManifest tracks hashed files for P2P sync (conflict-preserving).
type SharedWorkspaceManifest struct {
	WorkspaceID     string                       `json:"workspace_id"`
	CircleID        string                       `json:"circle_id"`
	Version         int64                        `json:"version"`
	UpdatedAt       string                       `json:"updated_at"`
	UpdatedByDevice string                       `json:"updated_by_device"`
	Files           map[string]SharedWorkspaceFile `json:"files"`
}

type SharedWorkspaceFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

func sharedWorkspaceRoot(home, circleID, workspaceID string) (string, error) {
	circleID = sanitizeSharedWorkspaceSegment(circleID)
	workspaceID = sanitizeSharedWorkspaceSegment(workspaceID)
	if circleID == "" || workspaceID == "" {
		return "", errors.New("invalid circle or workspace id")
	}
	return filepath.Join(circlesDir(home), circleID, "workspaces", workspaceID), nil
}

func sanitizeSharedWorkspaceSegment(seg string) string {
	seg = strings.TrimSpace(seg)
	if seg == "" {
		return ""
	}
	for _, r := range seg {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-', r == '_':
		default:
			return ""
		}
	}
	if strings.Contains(seg, "..") {
		return ""
	}
	return seg
}

// resolveSharedWorkspacePath maps a relative path inside a workspace root with anti-traversal and symlink checks.
func resolveSharedWorkspacePath(root, rel string) (string, error) {
	root = filepath.Clean(root)
	if root == "" {
		return "", errors.New("workspace root required")
	}
	rel = strings.TrimSpace(filepath.ToSlash(rel))
	rel = strings.Trim(rel, "/")
	if rel == "" {
		return root, nil
	}
	if strings.Contains(rel, "..") {
		return "", errors.New(errMsgPathTraversal)
	}
	candidate := filepath.Join(root, filepath.FromSlash(rel))
	candidate = filepath.Clean(candidate)
	rootPrefix := root + string(os.PathSeparator)
	if candidate != root && !strings.HasPrefix(candidate, rootPrefix) {
		return "", errors.New(errMsgPathTraversal)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		// Uniform handling: missing leaf paths still use the prefix-checked candidate.
		if !sharedWorkspacePathWithinRoot(candidate, root, rootPrefix) {
			return "", errors.New(errMsgPathTraversal)
		}
		return candidate, nil
	}
	resolvedRootPrefix := resolvedRoot + string(os.PathSeparator)
	if resolved != resolvedRoot && !strings.HasPrefix(resolved, resolvedRootPrefix) {
		return "", errors.New("symlink escapes workspace root")
	}
	return resolved, nil
}

func sharedWorkspacePathWithinRoot(candidate, root, rootPrefix string) bool {
	candidate = filepath.Clean(candidate)
	root = filepath.Clean(root)
	if candidate == root {
		return true
	}
	if rootPrefix == "" {
		rootPrefix = root + string(os.PathSeparator)
	}
	return strings.HasPrefix(candidate, rootPrefix)
}

func writeSharedWorkspaceFile(root, rel string, data []byte, mode os.FileMode) error {
	path, err := resolveSharedWorkspacePath(root, rel)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, mode)
}

func createSharedWorkspaceFile(root, rel string, data []byte, mode os.FileMode) error {
	path, err := resolveSharedWorkspacePath(root, rel)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

func defaultSharedWorkspaceLayout() map[string]string {
	return map[string]string{
		"context.md":     "# Shared workspace context\n",
		"decisions.md":   "# Decisions\n",
		"tasks.md":       "# Tasks\n",
		"references.json": "[]\n",
	}
}

// EnsureSharedWorkspace creates the circle workspace tree and seed files.
func EnsureSharedWorkspace(home, circleID, workspaceID, deviceID string) (string, error) {
	root, err := sharedWorkspaceRoot(home, circleID, workspaceID)
	if err != nil {
		return "", err
	}
	if err := hookMkdirAll(root, 0o700); err != nil {
		return "", err
	}
	for _, sub := range []string{"artifacts", "sessions"} {
		if err := hookMkdirAll(filepath.Join(root, sub), 0o700); err != nil {
			return "", err
		}
	}
	if err := seedSharedWorkspaceFiles(root); err != nil {
		return "", err
	}
	if err := ensureSharedWorkspaceManifest(root, circleID, workspaceID, deviceID); err != nil {
		return "", err
	}
	return root, nil
}

func seedSharedWorkspaceFiles(root string) error {
	for rel, content := range defaultSharedWorkspaceLayout() {
		if err := createSharedWorkspaceFile(root, rel, []byte(content), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func ensureSharedWorkspaceManifest(root, circleID, workspaceID, deviceID string) error {
	m := SharedWorkspaceManifest{
		WorkspaceID: workspaceID, CircleID: circleID, Version: 1,
		UpdatedAt: terminalNow(), UpdatedByDevice: deviceID, Files: map[string]SharedWorkspaceFile{},
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return createSharedWorkspaceFile(root, sharedWorkspaceManifestFile, raw, 0o600)
}

func readSharedWorkspaceManifest(root string) (SharedWorkspaceManifest, error) {
	path, err := resolveSharedWorkspacePath(root, sharedWorkspaceManifestFile)
	if err != nil {
		return SharedWorkspaceManifest{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return SharedWorkspaceManifest{}, err
	}
	var m SharedWorkspaceManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return SharedWorkspaceManifest{}, err
	}
	if m.Files == nil {
		m.Files = map[string]SharedWorkspaceFile{}
	}
	return m, nil
}

func writeSharedWorkspaceManifest(root string, m SharedWorkspaceManifest) error {
	if m.Files == nil {
		m.Files = map[string]SharedWorkspaceFile{}
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeSharedWorkspaceFile(root, sharedWorkspaceManifestFile, raw, 0o600)
}

func hashSharedWorkspaceFile(path string) (SharedWorkspaceFile, error) {
	info, err := os.Stat(path)
	if err != nil {
		return SharedWorkspaceFile{}, err
	}
	if info.Size() > sharedWorkspaceMaxBytes {
		return SharedWorkspaceFile{}, fmt.Errorf("file too large for workspace sync")
	}
	f, err := os.Open(path)
	if err != nil {
		return SharedWorkspaceFile{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return SharedWorkspaceFile{}, err
	}
	return SharedWorkspaceFile{
		Path:   filepath.Base(path),
		SHA256: hex.EncodeToString(h.Sum(nil)),
		Size:   n,
	}, nil
}

// WriteSharedWorkspaceArtifact stores a bundle or checkpoint under artifacts/ and updates manifest (no silent overwrite on hash mismatch).
func WriteSharedWorkspaceArtifact(home, circleID, workspaceID, deviceID, relName string, payload []byte) (SharedWorkspaceFile, error) {
	root, err := EnsureSharedWorkspace(home, circleID, workspaceID, deviceID)
	if err != nil {
		return SharedWorkspaceFile{}, err
	}
	rel := filepath.ToSlash(filepath.Join("artifacts", filepath.Base(relName)))
	if _, err := resolveSharedWorkspacePath(root, rel); err != nil {
		return SharedWorkspaceFile{}, err
	}
	if len(payload) > sharedWorkspaceMaxBytes {
		return SharedWorkspaceFile{}, fmt.Errorf("artifact exceeds workspace quota")
	}
	sum := sha256.Sum256(payload)
	newHash := hex.EncodeToString(sum[:])
	entry := SharedWorkspaceFile{Path: rel, SHA256: newHash, Size: int64(len(payload))}
	manifest, err := readSharedWorkspaceManifest(root)
	if err != nil {
		return SharedWorkspaceFile{}, err
	}
	if prev, ok := manifest.Files[rel]; ok && prev.SHA256 != "" && prev.SHA256 != newHash {
		conflictRel := strings.TrimSuffix(rel, filepath.Ext(rel)) + ".conflict-" + randomToken(6) + filepath.Ext(rel)
		if _, err := resolveSharedWorkspacePath(root, conflictRel); err != nil {
			return SharedWorkspaceFile{}, err
		}
		if err := writeSharedWorkspaceFile(root, conflictRel, payload, 0o600); err != nil {
			return SharedWorkspaceFile{}, err
		}
		entry.Path = conflictRel
		rel = conflictRel
	} else if err := writeSharedWorkspaceFile(root, rel, payload, 0o600); err != nil {
		return SharedWorkspaceFile{}, err
	}
	manifest.Files[rel] = entry
	manifest.Version++
	manifest.UpdatedAt = terminalNow()
	manifest.UpdatedByDevice = deviceID
	if err := writeSharedWorkspaceManifest(root, manifest); err != nil {
		return SharedWorkspaceFile{}, err
	}
	return entry, nil
}

// WorkspaceSyncPlan describes P2P hash sync actions (stub transport; callers apply locally).
type WorkspaceSyncPlan struct {
	Pull []string `json:"pull"`
	Push []string `json:"push"`
}

// PlanWorkspaceHashSync compares local and remote manifests without overwriting differing hashes.
func PlanWorkspaceHashSync(local, remote SharedWorkspaceManifest) WorkspaceSyncPlan {
	plan := WorkspaceSyncPlan{}
	for path, rf := range remote.Files {
		lf, ok := local.Files[path]
		if !ok || lf.SHA256 != rf.SHA256 {
			plan.Pull = append(plan.Pull, path)
		}
	}
	for path, lf := range local.Files {
		rf, ok := remote.Files[path]
		if !ok || rf.SHA256 != lf.SHA256 {
			plan.Push = append(plan.Push, path)
		}
	}
	return plan
}
