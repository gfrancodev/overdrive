package menu

import "testing"

func TestDisconnectedOffersStart(t *testing.T) {
	view := Build(Input{})
	if view.StatusText != "Status: disconnected" || view.Status != Disconnected {
		t.Fatalf("%+v", view)
	}
	if view.MeshText != "Mesh: unknown" {
		t.Fatal(view.MeshText)
	}
	if view.Power.Title != "Start Overdrive" || !view.Power.Enabled || view.Power.Action != ActionStart {
		t.Fatalf("power %+v", view.Power)
	}
	if view.Open.Enabled || view.Messages.Enabled || view.Messages.Title != "Messages" || view.MeshToggle.Enabled {
		t.Fatalf("items %+v", view)
	}
	if view.Messages.Title != view.Messages.Tooltip || view.StatusText == "" {
		t.Fatal("accessible text")
	}
}

func TestRunningPauseAndPeerCount(t *testing.T) {
	view := Build(Input{
		Connected: true, Mesh: "running", PeersKnown: true, PeerCount: 2, CanOpen: true,
	})
	if view.StatusText != "Status: running" {
		t.Fatal(view.StatusText)
	}
	if view.MeshText != "Mesh: 2 peers" {
		t.Fatal(view.MeshText)
	}
	if view.MeshToggle.Title != "Pause Mesh" || view.MeshToggle.Action != ActionPause {
		t.Fatalf("toggle %+v", view.MeshToggle)
	}
	if view.Power.Action != ActionStop || view.Open.Action != ActionOpen || view.Prefs.Action != ActionPreferences {
		t.Fatalf("%+v", view)
	}
	one := Build(Input{Connected: true, PeersKnown: true, PeerCount: 1, CanOpen: true})
	if one.MeshText != "Mesh: 1 peer" {
		t.Fatal(one.MeshText)
	}
}

func TestPausedShowsResume(t *testing.T) {
	view := Build(Input{Connected: true, Mesh: "paused", PeersKnown: true, PeerCount: 0, CanOpen: true})
	if view.Status != Running {
		t.Fatal(view.Status)
	}
	if view.MeshToggle.Title != "Resume Mesh" || view.MeshToggle.Action != ActionResume {
		t.Fatalf("%+v", view.MeshToggle)
	}
	if view.MeshText != "Mesh: 0 peers" {
		t.Fatal(view.MeshText)
	}
}

func TestSyncingAndErrorFromLinks(t *testing.T) {
	syncing := DeriveStatus(true, false, []Link{{State: "ok"}, {State: "syncing"}})
	if syncing != Syncing || syncing.Text() != "Status: syncing" {
		t.Fatal(syncing)
	}
	failed := DeriveStatus(true, false, []Link{{State: "syncing"}, {State: "timeout"}})
	if failed != Error || failed.Text() != "Status: error" {
		t.Fatal(failed)
	}
	if DeriveStatus(true, true, nil) != Error {
		t.Fatal("http error")
	}
}

func TestMessagesWithoutCountStayDisabled(t *testing.T) {
	view := Build(Input{Connected: true, CanOpen: true, UnreadKnown: false})
	if view.Messages.Enabled || view.Messages.Title != "Messages" || view.Messages.Action != "" {
		t.Fatalf("%+v", view.Messages)
	}
	known := Build(Input{Connected: true, CanOpen: true, UnreadKnown: true, Unread: 4})
	if !known.Messages.Enabled || known.Messages.Title != "Messages: 4" || known.Messages.Action != ActionMessages {
		t.Fatalf("%+v", known.Messages)
	}
}

func TestRestartIsOfferedWhileTheCoreAnswers(t *testing.T) {
	for name, in := range map[string]Input{
		"running":    {Connected: true, Mesh: "running"},
		"paused":     {Connected: true, Mesh: "paused"},
		"http error": {Connected: true, HTTPError: true},
	} {
		view := Build(in)
		if view.Restart.Title != "Restart Overdrive" || !view.Restart.Enabled || view.Restart.Action != ActionRestart {
			t.Fatalf("%s: restart %+v", name, view.Restart)
		}
	}
	// With the Core down there is nothing to restart; Start Overdrive covers it.
	if view := Build(Input{}); view.Restart.Enabled || view.Restart.Action != "" || view.Restart.Title != "Restart Overdrive" {
		t.Fatalf("down: restart %+v", view.Restart)
	}
}
