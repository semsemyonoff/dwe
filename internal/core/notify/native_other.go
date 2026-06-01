//go:build !darwin

package notify

import "github.com/gen2brain/beeep"

func init() {
	beeep.AppName = "DWE"
}

// osNotify on non-darwin platforms delegates straight to beeep, which
// handles Linux (dbus / notify-send) and Windows (toast) with the icon
// rendered correctly out of the box. The darwin path needs custom
// wiring — see native_darwin.go.
var osNotify = beeep.Notify
