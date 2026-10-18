package main

import (
	"sync"
	"testing"
)

func TestConcurrentCircleWritersDoNotUndoAKeyAdoption(t *testing.T) {
	home := meshHome(t)
	f := newRekeyFixture(t)
	writeIdentityFile(t, home, f.member.id)
	for round := 0; round < 20; round++ {
		saveTestCircle(t, home, f.circle)
		rotated, _, err := rekeyCircle(f.circle, f.creator.id, f.creator.priv)
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = mergePeerRoster(home, f.circle.ID, []CircleMember{{DeviceID: f.creator.id.DeviceID, Hostname: "creator-laptop"}})
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = applyKeysMaterial(home, f.circle.ID, nil, rotated.KeyUpdate)
		}()
		wg.Wait()
		got, err := loadCircle(home, f.circle.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.GroupKey != rotated.GroupKey {
			t.Fatalf("round %d: a concurrent roster write undid the key adoption", round)
		}
	}
}
