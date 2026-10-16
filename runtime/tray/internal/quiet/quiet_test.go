package quiet

import (
	"errors"
	"testing"
)

func TestPaused(t *testing.T) {
	if Paused(false, false) {
		t.Fatal("expected banners")
	}
	if !Paused(true, false) || !Paused(false, true) {
		t.Fatal("expected quiet")
	}
}

func TestGnomeAndDarwin(t *testing.T) {
	if GnomeBannersDisabled("true", nil) || GnomeBannersDisabled("", errors.New("missing")) {
		t.Fatal("gnome")
	}
	if !GnomeBannersDisabled("false\n", nil) {
		t.Fatal("gnome off")
	}
	if DarwinDoNotDisturb("0", nil) || DarwinDoNotDisturb("", errors.New("missing")) {
		t.Fatal("darwin")
	}
	if !DarwinDoNotDisturb("1", nil) {
		t.Fatal("darwin on")
	}
	if WindowsToastsDisabled(1, true) || WindowsToastsDisabled(0, false) {
		t.Fatal("windows")
	}
	if !WindowsToastsDisabled(0, true) {
		t.Fatal("windows off")
	}
}
