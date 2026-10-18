package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

type folderListBody struct {
	Folders     []circleFolderView `json:"folders"`
	Suggestions []circleFolderView `json:"suggestions"`
	Circle      circleView         `json:"circle"`
}

func folderAPI(t *testing.T, home, method, path string, body any, want int) folderListBody {
	t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	rec := inviteHTTP(t, home, method, path, "127.0.0.1:9", raw)
	if rec.Code != want {
		t.Fatalf("%s %s: got %d, want %d: %s", method, path, rec.Code, want, rec.Body.String())
	}
	var out folderListBody
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]int{}
	for _, s := range got {
		seen[s]++
	}
	for _, s := range want {
		if seen[s] == 0 {
			return false
		}
		seen[s]--
	}
	return true
}

func folderPaths(views []circleFolderView) []string {
	out := []string{}
	for _, v := range views {
		out = append(out, v.Path)
	}
	return out
}

func TestFolderAPIAddsListsAndRemovesAFolder(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	project := filepath.Join(t.TempDir(), "api")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(project)
	base := "/api/circles/" + circle.ID + "/folders"

	added := folderAPI(t, home, http.MethodPost, base, map[string]string{"path": project}, http.StatusCreated)
	if got := folderPaths(added.Folders); len(got) != 1 || got[0] != resolved {
		t.Fatalf("added folders = %v, want [%s]", got, resolved)
	}
	if added.Folders[0].Name != "api" || !added.Folders[0].Exists {
		t.Fatalf("folder view must carry its name and whether it exists: %+v", added.Folders[0])
	}
	if loaded, _ := loadCircle(home, circle.ID); !folderAllowed(loaded, project) {
		t.Fatal("a folder added from the dashboard must be shared")
	}
	if listed := folderAPI(t, home, http.MethodGet, base, nil, http.StatusOK); len(listed.Folders) != 1 {
		t.Fatalf("list must show the shared folder: %+v", listed)
	}

	folderAPI(t, home, http.MethodPost, base, map[string]string{"path": project}, http.StatusCreated)
	if loaded, _ := loadCircle(home, circle.ID); len(loaded.AllowedFolders) != 1 {
		t.Fatalf("adding the same folder twice must not duplicate it: %v", loaded.AllowedFolders)
	}

	removed := folderAPI(t, home, http.MethodDelete, base+"?path="+url.QueryEscape(resolved), nil, http.StatusOK)
	if len(removed.Folders) != 0 {
		t.Fatalf("removed folder still listed: %+v", removed.Folders)
	}
	if loaded, _ := loadCircle(home, circle.ID); folderAllowed(loaded, project) {
		t.Fatal("a removed folder must stop being shared")
	}
	folderAPI(t, home, http.MethodDelete, base+"?path="+url.QueryEscape(resolved), nil, http.StatusNotFound)
}

func TestFolderAPIOnlySharesProjectFolders(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	file := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := "/api/circles/" + circle.ID + "/folders"
	for name, path := range map[string]string{
		"empty":         "",
		"relative":      "src/app",
		"missing":       filepath.Join(t.TempDir(), "gone"),
		"file":          file,
		"root":          string(filepath.Separator),
		"the user home": userHome,
	} {
		t.Run(name, func(t *testing.T) {
			folderAPI(t, home, http.MethodPost, base, map[string]string{"path": path}, http.StatusBadRequest)
		})
	}
	if loaded, _ := loadCircle(home, circle.ID); len(loaded.AllowedFolders) != 0 {
		t.Fatalf("a rejected folder must not be shared: %v", loaded.AllowedFolders)
	}

	inside := filepath.Join(userHome, "code", "web")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	added := folderAPI(t, home, http.MethodPost, base, map[string]string{"path": "~/code/web"}, http.StatusCreated)
	if want, _ := filepath.EvalSymlinks(inside); len(added.Folders) != 1 || added.Folders[0].Path != want {
		t.Fatalf("~ must expand to the user's home: %+v", added.Folders)
	}

	folderAPI(t, home, http.MethodGet, "/api/circles/circle_missing/folders", nil, http.StatusNotFound)
	rec := inviteHTTP(t, home, http.MethodGet, base, "203.0.113.9:4000", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a remote browser must not manage folders: %d", rec.Code)
	}
}

func TestFolderSuggestionsComeFromProjectsThisDeviceWorkedOn(t *testing.T) {
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	circle, _ := createCircle(home, "team", id, priv)
	web := filepath.Join(t.TempDir(), "web")
	api := filepath.Join(t.TempDir(), "api")
	for _, dir := range []string{web, api} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e, err := newEngine(home)
	if err != nil {
		t.Fatal(err)
	}
	for i, m := range []Memory{
		{SourceFolder: web, Origin: "local"},
		{SourceFolder: api, Origin: "local"},
		{SourceFolder: filepath.Join(t.TempDir(), "deleted"), Origin: "local"},
		{SourceFolder: "/srv/peer-only", Origin: "peer", PeerDeviceID: "dev_other"},
	} {
		m.ID, m.Kind, m.Status, m.Scope, m.ScopeID = "m"+string(rune('a'+i)), "lesson", "active", "repository", "github.com/acme/r"
		m.Subject, m.Content, m.CreatedAt, m.UpdatedAt = "s", "c", nowRFC3339(), nowRFC3339()
		if err := e.upsertMemory(m); err != nil {
			t.Fatal(err)
		}
	}
	e.Close()
	if _, err := addAllowedFolder(home, circle.ID, api, id); err != nil {
		t.Fatal(err)
	}
	out := folderAPI(t, home, http.MethodGet, "/api/circles/"+circle.ID+"/folders", nil, http.StatusOK)
	webResolved, _ := filepath.EvalSymlinks(web)
	if got := folderPaths(out.Suggestions); len(got) != 1 || got[0] != webResolved {
		t.Fatalf("suggestions = %v, want only the known, existing, not yet shared project %s", got, webResolved)
	}
}

// revokeFixture is a circle this device created with members B and D it invited, and C that B invited.
// B and D published key-agreement keys; C runs a core that predates key rotation.
type revokeFixture struct {
	home    string
	circle  Circle
	b, c, d proofDevice
}

func newRevokeFixture(t *testing.T) revokeFixture {
	t.Helper()
	home := t.TempDir()
	setupTestEnv(t, home)
	id, priv, _ := loadOrCreateIdentity(home)
	self := proofDevice{id: id, priv: priv}
	circle, err := createCircle(home, "team", id, priv)
	if err != nil {
		t.Fatal(err)
	}
	b, c, d := newProofDevice(t), newProofDevice(t), newProofDevice(t)
	circle = circle.admit(b.member(), self.admit(t, circle.ID, b))
	circle = circle.admit(d.member(), self.admit(t, circle.ID, d))
	circle = circle.admit(c.member(), b.admit(t, circle.ID, c))
	var records []DeviceKexRecord
	for _, dev := range []proofDevice{b, d} {
		rec, err := selfKexRecord(dev.id, dev.priv)
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, rec)
	}
	circle = circle.ingestKexRecords(records)
	if err := saveCircle(home, circle); err != nil {
		t.Fatal(err)
	}
	return revokeFixture{home: home, circle: circle, b: b, c: c, d: d}
}

func (f revokeFixture) memberPath(dev proofDevice) string {
	return "/api/circles/" + f.circle.ID + "/members/" + dev.id.DeviceID + "/revoke"
}

func revokeCall(t *testing.T, home, method, path string, body any, want int) revokeView {
	t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	rec := inviteHTTP(t, home, method, path, "127.0.0.1:9", raw)
	if rec.Code != want {
		t.Fatalf("%s %s: got %d, want %d: %s", method, path, rec.Code, want, rec.Body.String())
	}
	var out revokeView
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

func TestRevokePreviewSaysWhoLosesAccessAndWhoIsLeftWithoutTheNewKey(t *testing.T) {
	f := newRevokeFixture(t)
	plain := revokeCall(t, f.home, http.MethodGet, f.memberPath(f.b), nil, http.StatusOK)
	if !sameStrings(plain.Targets, []string{f.b.id.DeviceID}) || !sameStrings(plain.Invitees, []string{f.c.id.DeviceID}) {
		t.Fatalf("revoking B alone: targets %v invitees %v", plain.Targets, plain.Invitees)
	}
	if !sameStrings(plain.CutOff, []string{f.c.id.DeviceID}) || plain.NextEpoch != 1 {
		t.Fatalf("C has no key-agreement key, so the rotation leaves it behind: cut_off %v next %d", plain.CutOff, plain.NextEpoch)
	}
	cascade := revokeCall(t, f.home, http.MethodGet, f.memberPath(f.b)+"?with_invitees=1", nil, http.StatusOK)
	if !sameStrings(cascade.Targets, []string{f.b.id.DeviceID, f.c.id.DeviceID}) || len(cascade.CutOff) != 0 {
		t.Fatalf("revoking B with invitees: targets %v cut_off %v", cascade.Targets, cascade.CutOff)
	}
	if loaded, _ := loadCircle(f.home, f.circle.ID); loaded.KeyEpoch != 0 || !activeIn(loaded, f.b) {
		t.Fatal("a preview must not change the circle")
	}
}

func TestRevokeFromTheDashboardRevokesAndRotatesTheKey(t *testing.T) {
	f := newRevokeFixture(t)
	out := revokeCall(t, f.home, http.MethodPost, f.memberPath(f.b), map[string]bool{"with_invitees": true}, http.StatusOK)
	if !sameStrings(out.Revoked, []string{f.b.id.DeviceID, f.c.id.DeviceID}) || out.Circle.KeyEpoch != 1 {
		t.Fatalf("revoke result: %+v", out)
	}
	loaded, _ := loadCircle(f.home, f.circle.ID)
	if activeIn(loaded, f.b) || activeIn(loaded, f.c) || !activeIn(loaded, f.d) {
		t.Fatal("B and C must be revoked and D kept")
	}
	if loaded.KeyEpoch != 1 || loaded.GroupKey == f.circle.GroupKey || loaded.KeyUpdate == nil {
		t.Fatal("revoking from the dashboard must rotate the circle key")
	}
	if _, ok := loaded.KeyUpdate.Entries[f.d.id.DeviceID]; !ok {
		t.Fatal("the remaining member must get the new key")
	}
	if _, ok := loaded.Revocations[f.b.id.DeviceID]; !ok {
		t.Fatal("the revocation must be signed")
	}
	for _, m := range out.Circle.Members {
		if m.DeviceID == f.b.id.DeviceID && !m.Revoked {
			t.Fatal("the returned circle must show B revoked")
		}
	}
	revokeCall(t, f.home, http.MethodPost, f.memberPath(f.b), map[string]bool{}, http.StatusNotFound)
}

func TestRevokeFromTheDashboardRefusesWhatTheCoreWouldRefuse(t *testing.T) {
	f := newRevokeFixture(t)
	id, _, _ := loadOrCreateIdentity(f.home)
	self := proofDevice{id: id}
	revokeCall(t, f.home, http.MethodGet, f.memberPath(self), nil, http.StatusBadRequest)
	revokeCall(t, f.home, http.MethodPost, f.memberPath(self), map[string]bool{}, http.StatusBadRequest)
	revokeCall(t, f.home, http.MethodPost, "/api/circles/"+f.circle.ID+"/members/dev_0000000000000000/revoke", map[string]bool{}, http.StatusNotFound)
	rec := inviteHTTP(t, f.home, http.MethodPost, f.memberPath(f.b), "203.0.113.9:4000", []byte("{}"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a remote browser must not revoke: %d", rec.Code)
	}

	// On a device that only joined the circle, only the creator may revoke.
	home := t.TempDir()
	setupTestEnv(t, home)
	joinerID, joinerPriv, _ := loadOrCreateIdentity(home)
	joiner := proofDevice{id: joinerID, priv: joinerPriv}
	creator := newProofDevice(t)
	other := newProofDevice(t)
	circle, err := createCircle(t.TempDir(), "team", creator.id, creator.priv)
	if err != nil {
		t.Fatal(err)
	}
	circle = circle.admit(joiner.member(), creator.admit(t, circle.ID, joiner))
	circle = circle.admit(other.member(), creator.admit(t, circle.ID, other))
	if err := saveCircle(home, circle); err != nil {
		t.Fatal(err)
	}
	path := "/api/circles/" + circle.ID + "/members/" + other.id.DeviceID + "/revoke"
	revokeCall(t, home, http.MethodGet, path, nil, http.StatusForbidden)
	revokeCall(t, home, http.MethodPost, path, map[string]bool{}, http.StatusForbidden)
	if loaded, _ := loadCircle(home, circle.ID); !activeIn(loaded, other) {
		t.Fatal("a refused revoke must leave the member in")
	}
}

func TestCircleViewShowsKeyEpochAndWhoCanReceiveNewKeys(t *testing.T) {
	f := newRevokeFixture(t)
	revokeCall(t, f.home, http.MethodPost, f.memberPath(f.d), map[string]bool{}, http.StatusOK)
	rec := inviteHTTP(t, f.home, http.MethodGet, "/api/circles", "127.0.0.1:9", nil)
	var out struct {
		Circles []circleView `json:"circles"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Circles) != 1 {
		t.Fatalf("list: %v %s", err, rec.Body.String())
	}
	view := out.Circles[0]
	if view.KeyEpoch != 1 || view.KeyRotatedAt == "" || view.PreviousKeyUntil == "" {
		t.Fatalf("after a rotation the view must show the epoch, when it rotated and the grace window: %+v", view)
	}
	receives := map[string]bool{}
	for _, m := range view.Members {
		receives[m.DeviceID] = m.ReceivesKeys
	}
	if !receives[f.b.id.DeviceID] || receives[f.c.id.DeviceID] {
		t.Fatalf("B published a key-agreement key and C did not: %v", receives)
	}
}
