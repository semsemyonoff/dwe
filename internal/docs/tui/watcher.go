package tui

import (
	"context"
	"log/slog"
	"os"

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
	}

	// Start the event pump goroutine
	// This goroutine reads from the watcher's Events and Errors channels
	// and exits when ctx is cancelled
	go w.eventPump()

	return w, nil
}

// eventPump reads from the watcher and logs file-changed events.
// This runs in its own goroutine and exits when w.ctx is cancelled.
// Close() owns the watcher lifetime — do not close here.
func (w *Watcher) eventPump() {
	for {
		select {
		case event, ok := <-w.watcher.Events:
			if !ok {
				// Channel closed
				return
			}
			// Log the event at debug level; the TUI will handle the notification
			slog.Debug("watcher event", "op", event.Op, "name", event.Name)

		case err, ok := <-w.watcher.Errors:
			if !ok {
				// Channel closed
				return
			}
			slog.Debug("watcher error", "err", err)

		case <-w.ctx.Done():
			// Context cancelled; exit the pump
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
	// Add the current directory
	if err := watcher.Add(path); err != nil {
		return err
	}

	// List entries to find subdirectories
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			fullPath := path + "/" + entry.Name()
			if err := walkAndWatch(watcher, fullPath); err != nil {
				// Continue on error; some dirs may be inaccessible
				slog.Debug("failed to add directory to watcher", "path", fullPath, "err", err)
			}
		}
	}

	return nil
}
