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

	// Create a cancellable context for the event pump
	ctx, cancel := context.WithCancel(ctx)

	w := &Watcher{
		watcher: watcher,
		root:    root,
		ctx:     ctx,
		cancel:  cancel,
		events:  make(chan FileChangedMsg, 16),
	}

	// Start the event pump goroutine
	go w.eventPump()

	return w, nil
}

// Events returns a channel that receives file-change notifications.
// The channel is closed when the Watcher is closed.
func (w *Watcher) Events() <-chan FileChangedMsg {
	return w.events
}

// eventPump reads from the watcher and forwards file-changed events.
// This runs in its own goroutine and exits when w.ctx is cancelled.
// Close() owns the watcher lifetime — do not close here.
func (w *Watcher) eventPump() {
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
			case <-w.ctx.Done():
				return
			default:
				// Drop the event if the consumer is behind; avoid blocking the pump.
			}

		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			slog.Debug("watcher error", "err", err)

		case <-w.ctx.Done():
			return
		}
	}
}

// Close stops the watcher and cleans up resources.
// It is safe to call Close() multiple times.
func (w *Watcher) Close() error {
	w.cancel()
	return w.watcher.Close()
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
