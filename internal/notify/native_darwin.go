//go:build darwin

package notify

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

// osNotify on darwin bypasses beeep so we can hand terminal-notifier
// the embedded PNG via both `-appIcon` and `-contentImage`. Modern
// macOS (Sonoma / Sequoia) silently ignores `-appIcon` for
// terminal-notifier and shows its bundle icon instead, so `-appIcon`
// is best-effort for older releases while `-contentImage` reliably
// renders the Devbox logo as a thumbnail in the notification body.
//
// If terminal-notifier is missing or fails we fall back to osascript,
// which can post a notification but cannot carry a custom icon — the
// alert appears with Script Editor's icon, same trade-off beeep makes.
var osNotify = darwinNotify

// terminalNotifierGroup is the value passed to `-group`. It controls
// notification stacking in Notification Center; matches beeep's
// historical `AppName` value so existing groupings stay stable.
const terminalNotifierGroup = "Devbox"

func darwinNotify(title, body string, icon any) error {
	iconBytes, ok := icon.([]byte)
	if !ok {
		return fmt.Errorf("notify: darwin backend expects icon []byte, got %T", icon)
	}

	iconPath, cleanup, iconErr := writeTempIcon(iconBytes)
	if iconErr != nil {
		// Without an icon we still want the notification to fire.
		slog.Debug("notify: temp icon write failed, sending without icon", "err", iconErr)
	} else {
		defer cleanup()
	}

	if tn, lookErr := exec.LookPath("terminal-notifier"); lookErr == nil {
		args := []string{
			"-title", title,
			"-message", body,
			"-group", terminalNotifierGroup,
		}
		if iconPath != "" {
			args = append(args, "-appIcon", iconPath, "-contentImage", iconPath)
		}
		if err := exec.Command(tn, args...).Run(); err == nil {
			return nil
		} else {
			slog.Debug("notify: terminal-notifier failed, falling back to osascript", "err", err)
		}
	}

	osa, err := exec.LookPath("osascript")
	if err != nil {
		return fmt.Errorf("notify: no terminal-notifier or osascript available: %w", err)
	}
	sanitize := func(s string) string {
		s = strings.ReplaceAll(s, `\`, `\\`)
		s = strings.ReplaceAll(s, `"`, `'`)
		s = strings.ReplaceAll(s, "\n", " ")
		s = strings.ReplaceAll(s, "\r", "")
		return s
	}
	script := fmt.Sprintf(`display notification "%s" with title "%s"`, sanitize(body), sanitize(title))
	return exec.Command(osa, "-e", script).Run()
}

func writeTempIcon(b []byte) (string, func(), error) {
	f, err := os.CreateTemp("", "devbox-notify-*.png")
	if err != nil {
		return "", nil, err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", nil, err
	}
	return f.Name(), func() { _ = os.Remove(f.Name()) }, nil
}
