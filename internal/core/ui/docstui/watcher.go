package docstui

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

// FileChangedMsg signals that a watched file has changed.
type FileChangedMsg struct {
	Path string
}

// Watcher wraps fsnotify.Watcher and manages the lifecycle of the watch goroutine.
type Watcher struct {
	watcher *fsnotify.Watcher
	root    string
	ctx     context.Context
	cancel  context.CancelFunc
	events  chan FileChangedMsg
	// done is closed by eventPump when it exits. Close() waits on it so that
	// callers are guaranteed both watcher goroutines have fully exited.
	done chan struct{}
}

// NewWatcher creates a new Watcher for the given root directory.
// The returned Watcher is ready to use; the internal event pump goroutine
// has already been started and will exit when ctx is cancelled.
func NewWatcher(ctx context.Context, root string) (*Watcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	// Add the root directory to the watch list recursively.
	err = walkAndWatch(watcher, root)
	if err != nil {
		_ = watcher.Close()
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)

	w := &Watcher{
		watcher: watcher,
		root:    root,
		ctx:     ctx,
		cancel:  cancel,
		events:  make(chan FileChangedMsg, 16),
		done:    make(chan struct{}),
	}

	go w.eventPump()

	return w, nil
}

// Events returns a channel that receives file-change notifications.
// The channel is closed when the Watcher is closed.
func (w *Watcher) Events() <-chan FileChangedMsg {
	return w.events
}

// eventPump reads from the watcher and forwards file-changed events.
// It exits ONLY when the underlying fsnotify Events channel is closed, which
// happens when the kqueue goroutine exits after w.watcher.Close(). This
// ordering is load-bearing: Close() waits on w.done, so when it unblocks, both
// the eventPump goroutine AND the kqueue goroutine have fully exited.
// (Context cancellation is intentionally not an exit path here; call Close() to
// stop the pump.)
func (w *Watcher) eventPump() {
	defer close(w.done)
	defer close(w.events)
	for {
		select {
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			slog.Debug("watcher event", "op", event.Op, "name", event.Name)
			select {
			case w.events <- FileChangedMsg{Path: event.Name}:
			default:
				// Drop the event if the consumer is behind; avoid blocking the pump.
			}

		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			slog.Debug("watcher error", "err", err)
		}
	}
}

// Close stops the watcher and blocks until both internal goroutines have
// exited. Closing the fsnotify watcher signals the kqueue goroutine to stop;
// when the kqueue goroutine exits it closes w.watcher.Events; eventPump sees
// the closed channel and returns, then closes w.done, which unblocks this
// method. Because <-w.done only unblocks after eventPump exits AND eventPump
// exits only after Events closes AND Events closes only after the kqueue
// goroutine exits, this provides a hard guarantee that both goroutines have
// fully stopped before Close() returns. Safe to call multiple times.
func (w *Watcher) Close() error {
	err := w.watcher.Close() // triggers: kqueue exit → Events close → eventPump exit → done close
	<-w.done                 // blocks until both goroutines have fully exited
	w.cancel()               // cleanup the context (idempotent)
	return err
}

// walkAndWatch recursively walks a directory and adds it (and all subdirs) to the watcher.
func walkAndWatch(watcher *fsnotify.Watcher, path string) error {
	if err := watcher.Add(path); err != nil {
		return err
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			fullPath := filepath.Join(path, entry.Name())
			if err := walkAndWatch(watcher, fullPath); err != nil {
				slog.Debug("failed to add directory to watcher", "path", fullPath, "err", err)
			}
		}
	}

	return nil
}
