package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	meshRunning        = "running"
	meshPaused         = "paused"
	memoryLocal        = "local"
	memoryCheckpointed = "checkpointed"
)

// coreStatus is the loopback admin view of the Core. Discovery, chat, and P2P
// sync follow the same switch; local memory stays available while they are off.
type coreStatus struct {
	URL       string `json:"url,omitempty"`
	Mesh      string `json:"mesh"`
	Discovery bool   `json:"discovery"`
	Chat      bool   `json:"chat"`
	Sync      bool   `json:"sync"`
	Memory    string `json:"memory"`
	Stopped   bool   `json:"stopped,omitempty"`
}

type meshStateFile struct {
	Mesh      string `json:"mesh"`
	Memory    string `json:"memory,omitempty"`
	UpdatedAt string `json:"updated_at"`
}

var (
	meshMu              sync.RWMutex
	meshPausedFlag      bool
	coreStopReady       bool
	coreStopped         bool
	coreDashboardServer *http.Server
	coreFinishOnce      sync.Once
	coreStopDelay       = 100 * time.Millisecond
)

func meshStatePath(home string) string {
	return filepath.Join(shareDir(home), "mesh-state.json")
}

func meshSuspended() bool {
	meshMu.RLock()
	defer meshMu.RUnlock()
	return meshPausedFlag
}

func meshAllowsDiscovery() bool { return meshTransportsOpen() }
func meshAllowsChat() bool      { return meshTransportsOpen() }
func meshAllowsSync() bool      { return meshTransportsOpen() }

func meshTransportsOpen() bool {
	meshMu.RLock()
	defer meshMu.RUnlock()
	return !meshPausedFlag && !coreStopped
}

func resetCoreLifecycle() {
	stopMeshServices()
	meshMu.Lock()
	srv := coreDashboardServer
	meshPausedFlag = false
	coreStopReady = false
	coreStopped = false
	coreDashboardServer = nil
	coreFinishOnce = sync.Once{}
	meshMu.Unlock()
	if srv != nil {
		_ = srv.Close()
	}
}

func loadMeshState(home string) {
	data, err := hookReadFile(meshStatePath(home))
	if err != nil {
		return
	}
	var st meshStateFile
	if json.Unmarshal(data, &st) != nil {
		return
	}
	meshMu.Lock()
	meshPausedFlag = st.Mesh == meshPaused
	meshMu.Unlock()
}

func pauseMesh(home string) error {
	if err := writeMeshState(home, meshPaused, memoryLocal); err != nil {
		return err
	}
	meshMu.Lock()
	meshPausedFlag = true
	meshMu.Unlock()
	suspendMeshTransports()
	return nil
}

func resumeMesh(home string) error {
	if err := writeMeshState(home, meshRunning, memoryLocal); err != nil {
		return err
	}
	meshMu.Lock()
	meshPausedFlag = false
	meshMu.Unlock()
	if meshServicesArmed() {
		startMeshServices(home)
	}
	return nil
}

func writeMeshState(home, mode, memory string) error {
	st := meshStateFile{Mesh: mode, Memory: memory, UpdatedAt: nowRFC3339()}
	if err := hookMkdirAll(shareDir(home), 0o700); err != nil {
		return err
	}
	data, err := hookJSONMarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return hookWriteFile(meshStatePath(home), data, 0o600)
}

func checkpointLocalMemory(home string) error {
	e, err := newEngine(home)
	if err != nil {
		return err
	}
	defer e.Close()
	var busy, logFrames, checkpointed int
	if err := e.db.QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &checkpointed); err != nil {
		return err
	}
	if busy != 0 {
		return fmt.Errorf("experience checkpoint busy")
	}
	return nil
}

func stopCore(home string) error {
	mode := meshRunning
	if meshSuspended() {
		mode = meshPaused
	}
	if err := writeMeshState(home, mode, memoryLocal); err != nil {
		return err
	}
	if err := hookCheckpointLocalMemory(home); err != nil {
		return err
	}
	if err := writeMeshState(home, mode, memoryCheckpointed); err != nil {
		return err
	}
	meshMu.Lock()
	coreStopReady = true
	coreStopped = true
	meshMu.Unlock()
	suspendMeshTransports()
	return nil
}

func finishCoreStop() {
	meshMu.RLock()
	ready := coreStopReady
	meshMu.RUnlock()
	if !ready {
		return
	}
	coreFinishOnce.Do(func() {
		meshMu.Lock()
		coreStopped = true
		srv := coreDashboardServer
		meshMu.Unlock()
		if shareListenerInst != nil {
			shareListenerInst.Close()
		}
		if srv != nil {
			_ = srv.Close()
		}
	})
}

func scheduleCoreStop() {
	go func() {
		time.Sleep(coreStopDelay)
		finishCoreStop()
	}()
}

func coreDashboardURL() (string, error) {
	addr, ok := shareUIAddr()
	if !ok {
		return "", errors.New("share ui is off")
	}
	// shareUIAddr binds the dashboard to loopback; HTTP is intentional for local UI only.
	return "http://" + addr + "/", nil // NOSONAR go:S5332
}

func currentCoreStatus() coreStatus {
	meshMu.RLock()
	paused := meshPausedFlag
	stopped := coreStopped
	meshMu.RUnlock()
	mode := meshRunning
	if paused {
		mode = meshPaused
	}
	memory := memoryLocal
	if stopped {
		memory = memoryCheckpointed
	}
	return coreStatus{
		Mesh:      mode,
		Discovery: meshAllowsDiscovery(),
		Chat:      meshAllowsChat(),
		Sync:      meshAllowsSync(),
		Memory:    memory,
		Stopped:   stopped,
	}
}

func writeCoreJSON(w http.ResponseWriter, st coreStatus) {
	w.Header().Set(headerContentType, "application/json")
	w.Header().Set(headerCacheControl, cacheNoStore)
	_ = json.NewEncoder(w).Encode(st)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func loopbackHostName(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func loopbackHTTPOrigin(origin string) bool {
	origin = strings.TrimSpace(origin)
	if origin == "" || origin == "null" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	if u.User != nil {
		return false
	}
	return loopbackHostName(u.Hostname())
}

// rejectRemoteCoreAdmin blocks non-loopback callers, DNS rebinding Host headers,
// and cross-site browser mutations against the local admin API.
func rejectRemoteCoreAdmin(w http.ResponseWriter, r *http.Request) bool {
	if !hookRemoteAllowed(r.RemoteAddr) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return true
	}
	host := r.Host
	if host == "" {
		if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			host = h
		}
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if !loopbackHostName(host) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return true
	}
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch:
		if strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")) == "cross-site" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return true
		}
		if origin := r.Header.Get("Origin"); origin != "" && !loopbackHTTPOrigin(origin) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return true
		}
	}
	return false
}

func registerCoreLifecycleRoutes(mux *http.ServeMux, home string) {
	mux.HandleFunc("/api/core/ui", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
			return
		}
		if rejectRemoteCoreAdmin(w, r) {
			return
		}
		serveCoreUI(w)
	})
	mux.HandleFunc("/api/mesh/pause", func(w http.ResponseWriter, r *http.Request) {
		serveMeshSwitch(w, r, home, pauseMesh)
	})
	mux.HandleFunc("/api/mesh/resume", func(w http.ResponseWriter, r *http.Request) {
		serveMeshSwitch(w, r, home, resumeMesh)
	})
	mux.HandleFunc("/api/core/stop", serveCoreStop(home))
}

func serveCoreUI(w http.ResponseWriter) {
	raw, err := coreDashboardURL()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	st := currentCoreStatus()
	st.URL = raw
	writeCoreJSON(w, st)
}

func serveMeshSwitch(w http.ResponseWriter, r *http.Request, home string, action func(string) error) {
	if r.Method != http.MethodPost {
		http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
		return
	}
	if rejectRemoteCoreAdmin(w, r) {
		return
	}
	if err := action(home); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeCoreJSON(w, currentCoreStatus())
}

func serveCoreStop(home string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
			return
		}
		if rejectRemoteCoreAdmin(w, r) {
			return
		}
		if err := stopCore(home); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		st := currentCoreStatus()
		st.Stopped = true
		st.Memory = memoryCheckpointed
		st.Discovery = false
		st.Chat = false
		st.Sync = false
		writeCoreJSON(w, st)
		scheduleCoreStop()
	}
}
