package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"
)

type writeCloser interface {
	io.Writer
	Close() error
}

// Test hooks — production defaults point at stdlib; tests may replace temporarily.
var (
	hookMkdirAll          = os.MkdirAll
	hookRename            = os.Rename
	hookChmod             = os.Chmod
	hookRemove            = os.Remove
	hookCreateTemp        = defaultCreateTemp
	hookIOCopy            = io.Copy
	hookUserHomeDir       = os.UserHomeDir
	hookFilepathAbs       = filepath.Abs
	hookRandRead          = rand.Read
	hookJSONMarshal       = json.Marshal
	hookJSONMarshalIndent = json.MarshalIndent
	hookReadDir           = os.ReadDir
	hookDBOpen            = sql.Open
	hookWriteFile         = os.WriteFile
	hookReadFile          = os.ReadFile
	hookEnsureFile        = ensureFileImpl
	hookNetDial           = defaultNetDial
	hookNetListen         = net.Listen
	hookOpenEngine        func() (*Engine, error)
	hookDeleteMemory      func(*Engine, string) error
	hookEnsureVectorIndex func(*Engine) error
	hookLedgerList        func(*Engine, Project, string) ([]LedgerEntry, error)
	hookRunGCScanErr      error
	hookFetchPeerSync     func(string, string, Circle, DeviceIdentity, ed25519.PrivateKey, string, string) (SyncResponse, error)
	hookShareListenWait   func(*shareListener)
	hookRecordMemory      func(*Engine, Project, string, string, string, string, float64, int, string, string, string) (Memory, error)
	hookEngineRecall      func(*Engine, Project, string, int, string) (RecallResponse, error)
	hookPeerMemoryCount   func(*Engine, string, string) (int, error)
	hookHashedCosine      func(string, string) float64
	hookMkdirTemp         = os.MkdirTemp
	hookScanMemory        func(scanner) (Memory, error)
	hookTryORTSession     func(string, string) (modelRunner, error)
	hookValidateMemoryHandler func(*Engine, *Memory, string, string, string, string) error
	hookConsolidateScanErr    error
	hookFTSScanErr            error
	hookColumnInfoScanErr     error
	hookMigrateAddColumn      func(*sql.DB, string, string) error
	hookShareNewEngine        func(home string) (*Engine, error)
	hookOpenDynamicLib        func(path string) (uintptr, error)
)

func defaultCreateTemp(dir, pattern string) (writeCloser, error) {
	return os.CreateTemp(dir, pattern)
}

func defaultNetDial(network, address string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout(network, address, timeout)
}

func resetHooksForTest() {
	hookMkdirAll = os.MkdirAll
	hookRename = os.Rename
	hookChmod = os.Chmod
	hookRemove = os.Remove
	hookCreateTemp = defaultCreateTemp
	hookIOCopy = io.Copy
	hookUserHomeDir = os.UserHomeDir
	hookFilepathAbs = filepath.Abs
	hookRandRead = rand.Read
	hookJSONMarshal = json.Marshal
	hookJSONMarshalIndent = json.MarshalIndent
	hookReadDir = os.ReadDir
	hookDBOpen = sql.Open
	hookWriteFile = os.WriteFile
	hookReadFile = os.ReadFile
	hookEnsureFile = ensureFileImpl
	hookNetDial = defaultNetDial
	hookNetListen = net.Listen
	hookOpenEngine = nil
	hookDeleteMemory = nil
	hookEnsureVectorIndex = nil
	hookLedgerList = nil
	hookRunGCScanErr = nil
	hookFetchPeerSync = nil
	hookShareListenWait = nil
	hookRecordMemory = nil
	hookEngineRecall = nil
	hookPeerMemoryCount = nil
	hookHashedCosine = nil
	hookMkdirTemp = os.MkdirTemp
	hookScanMemory = nil
	hookTryORTSession = nil
	hookValidateMemoryHandler = nil
	hookConsolidateScanErr = nil
	hookFTSScanErr = nil
	hookColumnInfoScanErr = nil
	hookMigrateAddColumn = nil
	hookShareNewEngine = nil
	hookOpenDynamicLib = nil
}
