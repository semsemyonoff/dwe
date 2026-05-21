package notify

import (
	"os"
	"slices"

	"devbox-cli/internal/ui"
	"devbox-cli/internal/userconfig"
)

// isInteractiveForNotify reports whether the current process should
// fire desktop notifications. Wrapped in a package-level var so tests
// can stub the detection without manipulating real env vars.
var isInteractiveForNotify = func() bool {
	if os.Getenv("CI") != "" {
		return false
	}
	if os.Getenv("DEVBOX_NONINTERACTIVE") != "" {
		return false
	}
	return ui.IsInteractiveFn(os.Stdin)
}

// New constructs a Notifier from a userconfig. Returns a disabled
// (noop) Notifier when:
//   - cfg is nil or master switch off
//   - no channels configured
//   - environment is non-interactive (CI / DEVBOX_NONINTERACTIVE / non-TTY)
//   - configured channels contain no recognised backend
//
// A disabled Notifier still satisfies the Notify(ctx, ev) contract
// (it just drops every event). A nil *Notifier is also safe to call.
func New(cfg *userconfig.Config) *Notifier {
	if cfg == nil || !cfg.NotifyEnabled || len(cfg.NotifyChannels) == 0 {
		return &Notifier{cfg: cfg, backend: noopBackend{}, enabled: false}
	}
	if !isInteractiveForNotify() {
		return &Notifier{cfg: cfg, backend: noopBackend{}, enabled: false}
	}
	b := pickBackend(cfg.NotifyChannels)
	if b == nil {
		return &Notifier{cfg: cfg, backend: noopBackend{}, enabled: false}
	}
	return &Notifier{cfg: cfg, backend: b, enabled: true}
}

// pickBackend chooses the first recognised channel. Returns nil when
// the channel list contains only unknown entries (forward-compat with
// future channels like telegram / webhook that the MVP binary doesn't
// know about yet).
func pickBackend(channels []string) backend {
	if slices.Contains(channels, "native") {
		return newNativeBackend()
	}
	return nil
}
