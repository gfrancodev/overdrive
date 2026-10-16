// Package quiet decides whether a desktop is in do-not-disturb.
package quiet

import "strings"

// Paused is true when banners are disabled or an inhibit flag is set.
func Paused(bannersDisabled, inhibited bool) bool {
	return bannersDisabled || inhibited
}

// GnomeBannersDisabled parses `gsettings get org.gnome.desktop.notifications show-banners`.
// A missing setting is not treated as do-not-disturb.
func GnomeBannersDisabled(output string, readErr error) bool {
	if readErr != nil {
		return false
	}
	return strings.TrimSpace(output) == "false"
}

// DarwinDoNotDisturb parses `defaults -currentHost read com.apple.notificationcenterui doNotDisturb`.
func DarwinDoNotDisturb(output string, readErr error) bool {
	if readErr != nil {
		return false
	}
	return strings.TrimSpace(output) == "1"
}

// WindowsToastsDisabled is the NOC_GLOBAL_SETTING_TOASTS_ENABLED registry value.
func WindowsToastsDisabled(enabled uint32, exists bool) bool {
	return exists && enabled == 0
}
