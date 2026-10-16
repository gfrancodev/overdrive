package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const shareListenProjectFile = "listen-project.json"

type shareListenProject struct {
	Root       string `json:"root"`
	Repository string `json:"repository,omitempty"`
	UpdatedAt  string `json:"updated_at"`
}

func isDefaultTerminalWorkingDirectory(dir string) bool {
	d := strings.TrimSpace(dir)
	return d == "" || d == "." || d == "./"
}

func sharedWorkspaceIDForRepository(repository string) string {
	repository = strings.TrimSpace(repository)
	if repository == "" {
		sum := sha256.Sum256([]byte("local"))
		return "ws_" + hex.EncodeToString(sum[:6])
	}
	seg := strings.ToLower(repository)
	seg = strings.ReplaceAll(seg, "/", "_")
	seg = strings.ReplaceAll(seg, ".", "_")
	seg = strings.Trim(seg, "_")
	if len(seg) > 56 {
		seg = seg[:56]
	}
	seg = sanitizeSharedWorkspaceSegment("ws_" + seg)
	if seg == "" {
		sum := sha256.Sum256([]byte(repository))
		return "ws_" + hex.EncodeToString(sum[:6])
	}
	return seg
}

func shareListenProjectPath(home string) string {
	return filepath.Join(shareDir(home), shareListenProjectFile)
}

func writeShareListenProjectRoot(home, cwd string) error {
	project, err := identifyProject(cwd)
	if err != nil {
		return err
	}
	root, err := hookFilepathAbs(project.Root)
	if err != nil {
		return err
	}
	payload := shareListenProject{
		Root:       root,
		Repository: project.Repository,
		UpdatedAt:  nowRFC3339(),
	}
	if err := hookMkdirAll(shareDir(home), 0o700); err != nil {
		return err
	}
	raw, err := hookJSONMarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return hookWriteFile(shareListenProjectPath(home), raw, 0o600)
}

func readShareListenProjectRoot(home string) (string, error) {
	data, err := hookReadFile(shareListenProjectPath(home))
	if err != nil {
		return "", err
	}
	var payload shareListenProject
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", err
	}
	root := strings.TrimSpace(payload.Root)
	if root == "" {
		return "", errors.New("listen project root missing")
	}
	return root, nil
}

func resolveTerminalProjectRoot(home, projectRoot string) (Project, error) {
	candidate := strings.TrimSpace(projectRoot)
	if candidate == "" {
		if persisted, err := readShareListenProjectRoot(home); err == nil {
			candidate = persisted
		}
	}
	if candidate == "" {
		candidate, _ = os.Getwd()
	}
	return identifyProject(candidate)
}

func experienceWorkSessionSegment(home string, project Project) string {
	e, err := newEngine(home)
	if err != nil {
		return "default"
	}
	defer e.Close()
	var sid string
	_ = e.db.QueryRow(`SELECT value FROM meta WHERE key = 'current_session_id'`).Scan(&sid)
	sid = strings.TrimSpace(sid)
	if sid == "" {
		started, err := e.startSession(project)
		if err != nil {
			return "default"
		}
		sid = started
	}
	seg := sanitizeSharedWorkspaceSegment(sid)
	if seg == "" {
		sum := sha256.Sum256([]byte(sid))
		return "sess_" + hex.EncodeToString(sum[:6])
	}
	return seg
}

// sharedWorkspaceShortLabel derives a human-readable repo slug from a workspace id (ws_github_com_org_repo → repo).
func sharedWorkspaceShortLabel(workspaceID string) string {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return "ws"
	}
	raw := strings.TrimPrefix(workspaceID, "ws_")
	parts := strings.Split(raw, "_")
	for i := len(parts) - 1; i >= 0; i-- {
		if seg := sanitizeSharedWorkspaceSegment(parts[i]); seg != "" {
			return seg
		}
	}
	sum := sha256.Sum256([]byte(workspaceID))
	return "ws_" + hex.EncodeToString(sum[:4])
}

func resolveCircleScopedPath(home, circleID string, elems ...string) (string, error) {
	circleID = sanitizeSharedWorkspaceSegment(circleID)
	if circleID == "" {
		return "", errors.New("invalid circle id")
	}
	for _, elem := range elems {
		if strings.Contains(filepath.ToSlash(elem), "..") {
			return "", errors.New(errMsgPathTraversal)
		}
	}
	parts := append([]string{circlesDir(home), circleID}, elems...)
	candidate := filepath.Clean(filepath.Join(parts...))
	base := filepath.Clean(circlesDir(home)) + string(os.PathSeparator)
	if !strings.HasPrefix(candidate, base) {
		return "", errors.New(errMsgPathTraversal)
	}
	return candidate, nil
}

// assertCircleScopedPath re-validates a path under circlesDir before symlink creation (gosecurity:S2083).
func assertCircleScopedPath(home, absolutePath string) error {
	base := filepath.Clean(circlesDir(home)) + string(os.PathSeparator)
	absolutePath = filepath.Clean(absolutePath)
	if !strings.HasPrefix(absolutePath, base) {
		return errors.New(errMsgPathTraversal)
	}
	return nil
}

func countCircleWorkspaces(home, circleID string) int {
	dir, err := resolveCircleScopedPath(home, circleID, "workspaces")
	if err != nil {
		return 0
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			n++
		}
	}
	return n
}

func circleTerminalSessionLinkPath(home, circleID, workspaceID, workName string, multiWorkspace bool) (string, error) {
	workName = sanitizeSharedWorkspaceSegment(workName)
	if workName == "" {
		return "", errors.New("invalid session name")
	}
	if multiWorkspace {
		label := sanitizeSharedWorkspaceSegment(sharedWorkspaceShortLabel(workspaceID))
		if label == "" {
			label = "ws"
		}
		return resolveCircleScopedPath(home, circleID, "sessions", label, workName)
	}
	return resolveCircleScopedPath(home, circleID, "sessions", workName)
}

// ensureCircleTerminalSessionLink exposes a shorter logical cwd (circles/.../sessions/...) while the
// synced tree remains under circles/.../workspaces/ws_*/sessions/...
func ensureCircleTerminalSessionLink(home, circleID, workspaceID, workName, sessionDir string) (string, error) {
	fallback := func() (string, error) {
		return canonicalTerminalWorkingDirectory(sessionDir)
	}
	multi := countCircleWorkspaces(home, circleID) > 1
	linkPath, err := circleTerminalSessionLinkPath(home, circleID, workspaceID, workName, multi)
	if err != nil {
		return fallback()
	}
	if err := hookMkdirAll(filepath.Dir(linkPath), 0o700); err != nil {
		return fallback()
	}
	targetAbs, err := hookFilepathAbs(sessionDir)
	if err != nil {
		return fallback()
	}
	if err := assertCircleScopedPath(home, linkPath); err != nil {
		return fallback()
	}
	_ = os.Remove(linkPath)
	if err := os.Symlink(targetAbs, linkPath); err != nil {
		return fallback()
	}
	logical, err := logicalTerminalWorkingDirectory(linkPath)
	if err != nil {
		return fallback()
	}
	return logical, nil
}

func resolveTerminalCircleID(home, circleID string, project Project) string {
	circleID = strings.TrimSpace(circleID)
	if circleID != "" {
		return circleID
	}
	if circle, ok := circleForProject(home, project.Root); ok {
		return circle.ID
	}
	return "local"
}

// ensureTerminalWorkSessionDirectory creates the Overdrive work-session folder under the
// circle shared workspace (sessions/<experience-session-id>) instead of the raw git checkout.
func ensureTerminalWorkSessionDirectory(home, deviceID, circleID, projectRoot string) (string, error) {
	project, err := resolveTerminalProjectRoot(home, projectRoot)
	if err != nil {
		return "", err
	}
	circleID = resolveTerminalCircleID(home, circleID, project)
	workspaceID := sharedWorkspaceIDForRepository(project.Repository)
	wsRoot, err := EnsureSharedWorkspace(home, circleID, workspaceID, deviceID)
	if err != nil {
		return "", err
	}
	workName := experienceWorkSessionSegment(home, project)
	rel := "sessions/" + workName
	sessionDir, err := resolveSharedWorkspacePath(wsRoot, rel)
	if err != nil {
		return "", err
	}
	if err := hookMkdirAll(sessionDir, 0o700); err != nil {
		return "", err
	}
	displayDir, err := ensureCircleTerminalSessionLink(home, circleID, workspaceID, workName, sessionDir)
	if err != nil {
		return "", err
	}
	return displayDir, nil
}

func (m *TerminalManager) resolveSessionWorkingDirectory(opts terminalCreateOpts) (string, error) {
	if !isDefaultTerminalWorkingDirectory(opts.WorkingDirectory) {
		return canonicalTerminalWorkingDirectory(opts.WorkingDirectory)
	}
	return ensureTerminalWorkSessionDirectory(m.home, m.device.DeviceID, opts.CircleID, opts.ProjectRoot)
}
