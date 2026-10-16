//go:build windows

package main

import (
	"os"
	"os/exec"

	"github.com/gfrancodev/overdrive/runtime/tray/internal/coreclient"
	"github.com/gfrancodev/overdrive/runtime/tray/internal/quiet"

	"golang.org/x/sys/windows/registry"
)

func notificationsPaused() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Notifications\Settings`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	value, _, err := key.GetIntegerValue("NOC_GLOBAL_SETTING_TOASTS_ENABLED")
	if err != nil {
		return false
	}
	return quiet.WindowsToastsDisabled(uint32(value), true)
}

// sendNotice shows a generic toast. The click target is the loopback UI URL.
// The toast XML is built in PowerShell from environment variables so the message
// body never has to be a shell argument.
func sendNotice(title, body, openURL string) error {
	safe, err := coreclient.ValidateUIURL(openURL)
	if err != nil {
		return nil
	}
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", windowsToastScript)
	cmd.Env = append(os.Environ(),
		"OVERDRIVE_TOAST_TITLE="+title,
		"OVERDRIVE_TOAST_BODY="+body,
		"OVERDRIVE_TOAST_URL="+safe,
	)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return errToast
	}
	return nil
}

var errToast = errString("notification was not sent")

type errString string

func (e errString) Error() string { return string(e) }

const windowsToastScript = `
$ErrorActionPreference = 'Stop'
function Esc([string]$s) {
  if ($null -eq $s) { return '' }
  return (($s -replace '&','&amp;') -replace '<','&lt;') -replace '>','&gt;' -replace '"','&quot;'
}
$xml = @"
<toast activationType="protocol" launch="$(Esc $env:OVERDRIVE_TOAST_URL)">
  <visual>
    <binding template="ToastGeneric">
      <text>$(Esc $env:OVERDRIVE_TOAST_TITLE)</text>
      <text>$(Esc $env:OVERDRIVE_TOAST_BODY)</text>
    </binding>
  </visual>
</toast>
"@
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] | Out-Null
$doc = New-Object Windows.Data.Xml.Dom.XmlDocument
$doc.LoadXml($xml)
$toast = [Windows.UI.Notifications.ToastNotification]::new($doc)
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('Overdrive').Show($toast)
`
