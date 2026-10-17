package main

import (
	"sync"
	"testing"
)

func TestCircleReadersNeverSeeAHalfWrittenFile(t *testing.T) {
	home := meshHome(t)
	creator, _ := testDevice(t)
	others := make([]DeviceIdentity, 0, 40)
	for i := 0; i < 40; i++ {
		d, _ := testDevice(t)
		others = append(others, d)
	}
	circle := saveTestCircle(t, home, testCircle(creator, others...))
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = saveCircle(home, circle)
			}
		}
	}()
	failures := 0
	for i := 0; i < 2000; i++ {
		if _, err := loadCircle(home, circle.ID); err != nil {
			failures++
		}
	}
	close(stop)
	wg.Wait()
	if failures > 0 {
		t.Fatalf("%d of 2000 reads saw a circle file in the middle of a write", failures)
	}
}
