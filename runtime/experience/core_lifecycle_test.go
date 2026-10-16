package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type coreLifecycleBody struct {
	URL       string `json:"url"`
	Mesh      string `json:"mesh"`
	Discovery bool   `json:"discovery"`
	Chat      bool   `json:"chat"`
	Sync      bool   `json:"sync"`
	Memory    string `json:"memory"`
	Stopped   bool   `json:"stopped"`
}

func TestLoopbackHTTPOriginAllowsEmptyAndNull(t *testing.T) {
	if !loopbackHTTPOrigin("") || !loopbackHTTPOrigin("null") {
		t.Fatal("missing origin should be treated as same-origin")
	}
	if loopbackHTTPOrigin("http://user@127.0.0.1:1") {
		t.Fatal("credentials in origin should be rejected")
	}
	if !loopbackHTTPOrigin("http://[::1]:7742") {
		t.Fatal("ipv6 loopback origin should be allowed")
	}
}

func TestMeshSwitchRejectsWrongMethod(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	rec := serveCore(t, home, http.MethodGet, "/api/mesh/pause", "127.0.0.1:9")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("pause GET %d", rec.Code)
	}
	rec = serveCore(t, home, http.MethodGet, "/api/core/stop", "127.0.0.1:9")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("stop GET %d", rec.Code)
	}
}

func TestCoreUIReturnsLoopbackURL(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	t.Setenv("OVERDRIVE_SHARE_LISTEN", "127.0.0.1:7741")
	t.Setenv("OVERDRIVE_SHARE_UI", "")

	rec := serveCore(t, home, http.MethodGet, "/api/core/ui", "127.0.0.1:9")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	body := decodeCoreBody(t, rec.Body.Bytes())
	if body.URL != "http://127.0.0.1:7742/" {
		t.Fatalf("url %q", body.URL)
	}
	if body.Mesh != "running" || !body.Discovery || !body.Chat || !body.Sync || body.Memory != "local" {
		t.Fatalf("running status %+v", body)
	}

	off := serveCore(t, home, http.MethodGet, "/api/core/ui", "10.1.1.1:9")
	if off.Code != http.StatusForbidden {
		t.Fatalf("remote ui %d", off.Code)
	}
	if strings.Contains(off.Body.String(), "http://") {
		t.Fatalf("remote received a url: %s", off.Body.String())
	}
}

func TestCoreAdminRejectsDNSRebindingHost(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	req := httptest.NewRequest(http.MethodGet, "/api/core/ui", nil)
	req.RemoteAddr = "127.0.0.1:9"
	req.Host = "evil.example:7742"
	rec := httptest.NewRecorder()
	shareDashboardHandler(home).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("rebinding host %d", rec.Code)
	}
}

func TestCoreAdminRejectsCrossSiteMutation(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	req := httptest.NewRequest(http.MethodPost, "/api/mesh/pause", nil)
	req.RemoteAddr = "127.0.0.1:9"
	req.Host = "127.0.0.1:7742"
	req.Header.Set("Origin", "https://attacker.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	shareDashboardHandler(home).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site %d", rec.Code)
	}
}

func TestCoreAdminRejectsNonLoopbackBeforeMutation(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/core-admin.git")
	rememberLocalLesson(t, home, repo, "Keep local memory")

	for _, path := range []string{"/api/mesh/pause", "/api/mesh/resume", "/api/core/stop"} {
		rec := serveCore(t, home, http.MethodPost, path, "10.4.4.4:9")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s status %d", path, rec.Code)
		}
	}
	if meshSuspended() {
		t.Fatal("lan caller paused mesh")
	}
	if _, err := os.Stat(meshStatePath(home)); !os.IsNotExist(err) {
		t.Fatalf("lan caller wrote mesh state: %v", err)
	}
	if countLessons(t, home, "Keep local memory") != 1 {
		t.Fatal("lan stop touched local memory")
	}

	ok := serveCore(t, home, http.MethodPost, "/api/mesh/pause", "[::1]:9")
	if ok.Code != http.StatusOK {
		t.Fatalf("ipv6 loopback %d %s", ok.Code, ok.Body.String())
	}
}

func TestPauseSuspendsMeshAndKeepsLocalMemory(t *testing.T) {
	home := t.TempDir()
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/pause-mesh.git")
	project := rememberLocalLesson(t, home, repo, "Keep local memory")
	sl, endpoint := startShareListenerForTest(t, home)
	defer sl.Close()
	armPeerSync(t, home, repo)

	if err := pauseMesh(home); err != nil {
		t.Fatal(err)
	}
	body := decodeCoreBody(t, serveCore(t, home, http.MethodPost, "/api/mesh/pause", "127.0.0.1:9").Body.Bytes())
	if body.Mesh != "paused" || body.Discovery || body.Chat || body.Sync || body.Memory != "local" {
		t.Fatalf("paused status %+v", body)
	}
	assertPeerSyncSuspended(t, home, project, endpoint)

	recordLocalLesson(t, home, project, "Written while paused")
	if countLessons(t, home, "Keep local memory") != 1 || countLessons(t, home, "Written while paused") != 1 {
		t.Fatal("pause dropped or blocked local memory")
	}
	if _, err := os.Stat(dbPath(home)); err != nil {
		t.Fatal(err)
	}
}

func TestResumeRestoresPeerSync(t *testing.T) {
	home := t.TempDir()
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/resume-mesh.git")
	project := rememberLocalLesson(t, home, repo, "Keep local memory")
	sl, endpoint := startShareListenerForTest(t, home)
	defer sl.Close()
	armPeerSync(t, home, repo)

	if err := pauseMesh(home); err != nil {
		t.Fatal(err)
	}
	rec := serveCore(t, home, http.MethodPost, "/api/mesh/resume", "127.0.0.1:9")
	if rec.Code != http.StatusOK {
		t.Fatalf("resume %d %s", rec.Code, rec.Body.String())
	}
	body := decodeCoreBody(t, rec.Body.Bytes())
	if body.Mesh != "running" || !body.Discovery || !body.Chat || !body.Sync || body.Memory != "local" {
		t.Fatalf("resumed status %+v", body)
	}
	if meshSuspended() {
		t.Fatal("still suspended")
	}
	if countLessons(t, home, "Keep local memory") != 1 {
		t.Fatal("resume dropped local memory")
	}

	dials := 0
	hookNetDial = func(string, string, time.Duration) (net.Conn, error) {
		dials++
		return nil, errors.New("refused")
	}
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.syncCirclePeers(project); err != nil {
		t.Fatal(err)
	}
	if dials == 0 {
		t.Fatal("resume did not dial peers")
	}
	if !peerConnStaysOpen(t, endpoint) {
		t.Fatal("resumed listener still dropping peers")
	}
}

func TestStopPersistsBeforeClosingCore(t *testing.T) {
	home := t.TempDir()
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/stop-core.git")
	rememberLocalLesson(t, home, repo, "Keep local memory")
	sl, _ := startShareListenerForTest(t, home)
	defer sl.Close()
	hookCheckpointLocalMemory = func(checkpointHome string) error {
		select {
		case <-sl.stop:
			t.Fatal("listener closed before checkpoint")
		default:
		}
		return checkpointLocalMemory(checkpointHome)
	}

	if err := stopCore(home); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(meshStatePath(home))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"memory": "checkpointed"`)) {
		t.Fatalf("state before close: %s", raw)
	}
	select {
	case <-sl.stop:
		t.Fatal("stopCore closed the listener before the response path")
	default:
	}

	rec := serveCore(t, home, http.MethodPost, "/api/core/stop", "127.0.0.1:9")
	if rec.Code != http.StatusOK {
		t.Fatalf("stop %d %s", rec.Code, rec.Body.String())
	}
	body := decodeCoreBody(t, rec.Body.Bytes())
	if !body.Stopped || body.Memory != "checkpointed" || body.Discovery || body.Chat || body.Sync {
		t.Fatalf("stop status %+v", body)
	}
	waitListenerStopped(t, sl)
	if countLessons(t, home, "Keep local memory") != 1 {
		t.Fatal("stop dropped local memory")
	}
}

func TestStopRefusesWhenPersistFails(t *testing.T) {
	home := t.TempDir()
	repo := makeTestRepo(t, filepath.Join(t.TempDir(), "repo"), "https://github.com/acme/stop-fail.git")
	rememberLocalLesson(t, home, repo, "Keep local memory")
	sl, _ := startShareListenerForTest(t, home)
	defer sl.Close()

	hookWriteFile = func(string, []byte, os.FileMode) error {
		return errors.New("disk full")
	}
	rec := serveCore(t, home, http.MethodPost, "/api/core/stop", "127.0.0.1:9")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("persist failure %d %s", rec.Code, rec.Body.String())
	}
	assertListenerStillOpen(t, sl)
	if countLessons(t, home, "Keep local memory") != 1 {
		t.Fatal("failed stop dropped local memory")
	}

	sl2, _ := startShareListenerForTest(t, home)
	defer sl2.Close()
	hookCheckpointLocalMemory = func(string) error { return errors.New("wal") }
	rec = serveCore(t, home, http.MethodPost, "/api/core/stop", "127.0.0.1:9")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("checkpoint failure %d %s", rec.Code, rec.Body.String())
	}
	assertListenerStillOpen(t, sl2)
	raw, err := os.ReadFile(meshStatePath(home))
	if err == nil && bytes.Contains(raw, []byte(`"memory": "checkpointed"`)) {
		t.Fatalf("checkpoint marked done after failure: %s", raw)
	}
}

func TestShareListenReloadsPausedMesh(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	t.Cleanup(func() { shareDashboardOnce = sync.Once{} })
	if err := pauseMesh(home); err != nil {
		t.Fatal(err)
	}
	meshMu.Lock()
	meshPausedFlag = false
	meshMu.Unlock()

	port := freeTCPPort(t)
	t.Setenv("OVERDRIVE_SHARE_LISTEN", "127.0.0.1:"+strconv.Itoa(port))
	t.Setenv("OVERDRIVE_SHARE_UI", "off")
	hookShareListenWait = func(sl *shareListener) {
		defer sl.Close()
		if !meshSuspended() {
			t.Error("paused mesh was not reloaded")
		}
	}
	if err := cmdShareListen([]string{"--cwd", home}); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureShareListenSkipsOccupiedPort(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "calls")
	runtime := writeFakeShareRuntime(t, logPath)
	holder := writePortHolder(t)
	runShareHookScript(t, runtime, `
"`+holder+`" &
holder_pid=$!
trap 'kill "$holder_pid" 2>/dev/null || true' EXIT
wait_for_port 7741
ensure_share_listen "$1"
`)
	if strings.Contains(readFileOrEmpty(logPath), "listen ") {
		t.Fatalf("started a second share listen: %s", readFileOrEmpty(logPath))
	}
}

func TestEnsureShareListenStartsOnlyOneProcess(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "calls")
	runtime := writeFakeShareRuntime(t, logPath)
	t.Cleanup(func() { killLoggedListeners(logPath) })
	runShareHookScript(t, runtime, `
trap 'kill $(jobs -p) 2>/dev/null || true' EXIT
ensure_share_listen "$1"
wait_for_port 7741
ensure_share_listen "$1"
sleep 0.4
`)
	if got := listenCount(readFileOrEmpty(logPath)); got != 1 {
		t.Fatalf("listen processes: %d\n%s", got, readFileOrEmpty(logPath))
	}
}

func serveCore(t *testing.T, home, method, path, remote string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = remote
	req.Host = "127.0.0.1:7742"
	rec := httptest.NewRecorder()
	shareDashboardHandler(home).ServeHTTP(rec, req)
	return rec
}

func decodeCoreBody(t *testing.T, raw []byte) coreLifecycleBody {
	t.Helper()
	var body coreLifecycleBody
	if err := json.Unmarshal(bytes.TrimSpace(raw), &body); err != nil {
		t.Fatalf("json: %v body %s", err, raw)
	}
	return body
}

func rememberLocalLesson(t *testing.T, home, repo, subject string) Project {
	t.Helper()
	setupTestEnv(t, home)
	project, err := identifyProject(repo)
	if err != nil {
		t.Fatal(err)
	}
	recordLocalLesson(t, home, project, subject)
	return project
}

func recordLocalLesson(t *testing.T, home string, project Project, subject string) {
	t.Helper()
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if _, err := e.recordMemory(memoryRecord{
		Project: project, Kind: "lesson", Scope: "repository", Subject: subject,
		Content: "local lesson stays on this machine", Confidence: 0.9, Priority: 50,
		Source: "verified_execution", Evidence: "core lifecycle",
	}); err != nil {
		t.Fatal(err)
	}
}

func countLessons(t *testing.T, home, subject string) int {
	t.Helper()
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	memories, err := e.listActiveMemories()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, m := range memories {
		if m.Subject == subject && m.Content == "local lesson stays on this machine" {
			n++
		}
	}
	return n
}

func armPeerSync(t *testing.T, home, repo string) {
	t.Helper()
	id, priv, err := loadOrCreateIdentity(home)
	if err != nil {
		t.Fatal(err)
	}
	circle, err := createCircle(home, "team", id, priv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := addAllowedFolder(home, circle.ID, repo, id); err != nil {
		t.Fatal(err)
	}
	circle, err = loadCircle(home, circle.ID)
	if err != nil {
		t.Fatal(err)
	}
	circle.PeerEndpoints = []string{"127.0.0.1:1"}
	if err := saveCircle(home, circle); err != nil {
		t.Fatal(err)
	}
}

func assertPeerSyncSuspended(t *testing.T, home string, project Project, endpoint string) {
	t.Helper()
	dials := 0
	hookNetDial = func(string, string, time.Duration) (net.Conn, error) {
		dials++
		return nil, errors.New("refused")
	}
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.syncCirclePeers(project); err != nil {
		t.Fatal(err)
	}
	if dials != 0 {
		t.Fatalf("paused mesh dialed %d peers", dials)
	}
	if peerConnStaysOpen(t, endpoint) {
		t.Fatal("paused listener kept the peer connection")
	}
}

func peerConnStaysOpen(t *testing.T, endpoint string) bool {
	t.Helper()
	conn, err := net.DialTimeout("tcp", endpoint, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, err = conn.Read(make([]byte, 1))
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func waitListenerStopped(t *testing.T, sl *shareListener) {
	t.Helper()
	select {
	case <-sl.stop:
	case <-time.After(time.Second):
		t.Fatal("listener still running")
	}
}

func assertListenerStillOpen(t *testing.T, sl *shareListener) {
	t.Helper()
	select {
	case <-sl.stop:
		t.Fatal("listener stopped")
	case <-time.After(150 * time.Millisecond):
	}
}

func hookLibrary(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "hooks", "run-hook.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	idx := strings.LastIndex(text, "hook=")
	if idx < 0 {
		t.Fatal("hook dispatcher missing")
	}
	return text[:idx]
}

func writeFakeShareRuntime(t *testing.T, logPath string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-runtime.py")
	script := `#!/usr/bin/env python3
import os, sys, socket, time
log = os.environ["CORE_LISTEN_LOG"]
args = sys.argv[1:]
def cmd(a, b):
    return len(args) >= 2 and args[0] == a and args[1] == b
if cmd("share", "status"):
    sys.stdout.write('{"mode": "ready"}\n')
    raise SystemExit(0)
if cmd("share", "listen"):
    with open(log, "a", encoding="utf-8") as fh:
        fh.write("listen %d\n" % os.getpid())
        fh.flush()
        os.fsync(fh.fileno())
    s = socket.socket()
    s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind(("127.0.0.1", 7741))
    s.listen(8)
    while True:
        time.sleep(0.5)
sys.stdout.write("{}\n")
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CORE_LISTEN_LOG", logPath)
	return path
}

func writePortHolder(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hold-7741.py")
	script := `#!/usr/bin/env python3
import socket
s = socket.socket()
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("127.0.0.1", 7741))
s.listen(8)
while True:
    conn, _ = s.accept()
    conn.close()
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func shareHookCanUnshare() bool {
	if os.Getenv("OVERDRIVE_TEST_DISABLE_UNSHARE") == "1" {
		return false
	}
	if _, err := exec.LookPath("unshare"); err != nil {
		return false
	}
	return exec.Command("unshare", "-Urn", "true").Run() == nil
}

func runShareHookScript(t *testing.T, runtime, body string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	isolated := shareHookCanUnshare()
	prefix := ""
	if isolated {
		prefix = "ip link set lo up\n"
	}
	script := hookLibrary(t) + "\n" + prefix + `
wait_for_port() {
  local port="$1" i
  for i in $(seq 1 50); do
    if (echo >/dev/tcp/127.0.0.1/"$port") >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.02
  done
  echo "port $port did not accept" >&2
  return 1
}
` + body
	argv := []string{"bash", "-s", "--", runtime}
	if isolated {
		argv = append([]string{"unshare", "-Urn"}, argv...)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = strings.NewReader(script)
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"CORE_LISTEN_LOG=" + os.Getenv("CORE_LISTEN_LOG"),
		"OVERDRIVE_SHARE_UI=127.0.0.1:7742",
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ensure_share_listen: %v\n%s", err, out)
	}
}

func readFileOrEmpty(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(raw)
}

func listenCount(raw string) int {
	n := 0
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "listen ") {
			n++
		}
	}
	return n
}

func killLoggedListeners(path string) {
	for _, line := range strings.Split(readFileOrEmpty(path), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != "listen" {
			continue
		}
		pid, err := strconv.Atoi(fields[1])
		if err != nil || pid <= 0 {
			continue
		}
		proc, err := os.FindProcess(pid)
		if err == nil {
			_ = proc.Kill()
		}
	}
}
