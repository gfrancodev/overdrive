package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"sync"
)

// Other devices reach this one only after the person allows it. Until then the Core listens on loopback, as the
// tray and the session hook start it. The choice lives in share/network.json, wins over the address the Core
// was started with, and is applied to a running Core without restarting it.

type networkSetting struct {
	LANAccess bool   `json:"lan_access"`
	UpdatedAt string `json:"updated_at"`
}

// networkView is what the dashboard shows: whether devices on the network can connect, the bind, and the
// address they dial.
type networkView struct {
	LANAccess bool   `json:"lan_access"`
	Listen    string `json:"listen"`
	Address   string `json:"address,omitempty"`
	LANIP     string `json:"lan_ip,omitempty"`
	Listening bool   `json:"listening"`
}

var errNetworkRebind = errors.New("could not accept network connections")

// listenOverride is the bind the network choice gave the running Core, when it differs from the one it was
// started with. Peers are told this address.
var listenOverride struct {
	sync.RWMutex
	addr string
}

func setListenOverride(addr string) {
	listenOverride.Lock()
	listenOverride.addr = addr
	listenOverride.Unlock()
}

func currentListenOverride() string {
	listenOverride.RLock()
	defer listenOverride.RUnlock()
	return listenOverride.addr
}

func networkSettingPath(home string) string {
	return filepath.Join(shareDir(home), "network.json")
}

// loadNetworkSetting returns the stored choice; ok is false when the person never made one.
func loadNetworkSetting(home string) (networkSetting, bool) {
	data, err := hookReadFile(networkSettingPath(home))
	if err != nil {
		return networkSetting{}, false
	}
	var s networkSetting
	if json.Unmarshal(data, &s) != nil {
		return networkSetting{}, false
	}
	return s, true
}

func saveNetworkSetting(home string, lan bool) error {
	if err := hookMkdirAll(shareDir(home), 0o700); err != nil {
		return err
	}
	data, err := hookJSONMarshalIndent(networkSetting{LANAccess: lan, UpdatedAt: nowRFC3339()}, "", "  ")
	if err != nil {
		return err
	}
	return hookWriteFile(networkSettingPath(home), append(data, '\n'), 0o600)
}

func restoreNetworkSetting(home string, prev networkSetting, had bool) {
	if !had {
		_ = hookRemove(networkSettingPath(home))
		return
	}
	_ = saveNetworkSetting(home, prev.LANAccess)
}

// listenForSetting applies the person's choice to the address the Core was started with. Allowing the network
// opens a loopback bind on every interface and keeps an explicit address; closing it brings any bind back to
// loopback on the same port.
func listenForSetting(raw string, setting networkSetting, chosen bool) string {
	if !chosen {
		return raw
	}
	host, port, err := net.SplitHostPort(normalizeListen(raw))
	if err != nil {
		return raw
	}
	loopback := isLoopbackHost(host)
	switch {
	case setting.LANAccess && loopback:
		return net.JoinHostPort(listenAllIPv4, port)
	case !setting.LANAccess && !loopback:
		return net.JoinHostPort("127.0.0.1", port)
	default:
		return raw
	}
}

func effectiveShareListen(home string) string {
	setting, chosen := loadNetworkSetting(home)
	return listenForSetting(shareListenAddr(), setting, chosen)
}

func bindAcceptsNetwork(addr string) bool {
	host, _, err := net.SplitHostPort(normalizeListen(addr))
	return err == nil && !isLoopbackHost(host)
}

// announcesNetwork is what discovery tells peers: whether this device accepts connections from them.
func announcesNetwork() bool {
	if sl := currentShareListener(); sl.listener() != nil {
		return bindAcceptsNetwork(sl.bindAddr())
	}
	return bindAcceptsNetwork(shareListenAddr())
}

func currentNetworkView(home string) networkView {
	view := networkView{Listen: effectiveShareListen(home), LANIP: firstLANIPv4()}
	if sl := currentShareListener(); sl.listener() != nil {
		view.Listen = sl.bindAddr()
		view.Listening = true
	}
	view.LANAccess = bindAcceptsNetwork(view.Listen)
	view.Address = publicEndpoint(normalizeListen(view.Listen))
	return view
}

// applyNetworkAccess stores the choice and moves a running Core onto it. A rebind that fails leaves the Core on
// its previous address and the previous choice on disk.
func applyNetworkAccess(home string, lan bool) (networkView, error) {
	prev, had := loadNetworkSetting(home)
	if err := saveNetworkSetting(home, lan); err != nil {
		return currentNetworkView(home), err
	}
	sl := currentShareListener()
	if sl.listener() == nil {
		return currentNetworkView(home), nil
	}
	_, port, err := net.SplitHostPort(sl.bindAddr())
	if err != nil {
		return currentNetworkView(home), err
	}
	host, _, _ := net.SplitHostPort(normalizeListen(effectiveShareListen(home)))
	target := net.JoinHostPort(host, port)
	if err := sl.rebind(target); err != nil {
		restoreNetworkSetting(home, prev, had)
		return currentNetworkView(home), fmt.Errorf("%w: %v", errNetworkRebind, err)
	}
	if target == normalizeListen(shareListenAddr()) {
		setListenOverride("")
	} else {
		setListenOverride(target)
	}
	restartDiscovery(home)
	return currentNetworkView(home), nil
}

func registerNetworkRoutes(mux *http.ServeMux, home string) {
	mux.HandleFunc("/api/network", func(w http.ResponseWriter, r *http.Request) {
		if rejectRemoteCoreAdmin(w, r) {
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeInviteJSON(w, http.StatusOK, currentNetworkView(home))
		case http.MethodPost:
			r.Body = http.MaxBytesReader(w, r.Body, 1024)
			var body struct {
				LANAccess *bool `json:"lan_access"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.LANAccess == nil {
				writeInviteError(w, http.StatusBadRequest, "say whether devices on the network may connect")
				return
			}
			view, err := applyNetworkAccess(home, *body.LANAccess)
			if err != nil {
				writeInviteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeInviteJSON(w, http.StatusOK, view)
		default:
			http.Error(w, methodNotAllowedMsg, http.StatusMethodNotAllowed)
		}
	})
}
