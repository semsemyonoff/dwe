package tui

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"sync"

	"devbox-cli/internal/docs/mermaid"
)

// WorkItem represents a diagram to be rendered.
type WorkItem struct {
	Source string
	Theme  mermaid.Theme
	Width  int
	Index  int // Diagram index for ordering
}

// ProgressMsg reports prefetch progress to the TUI.
type ProgressMsg struct {
	Rendered int
	Total    int
}

// Prefetch manages background mermaid rendering with a bounded worker pool.
type Prefetch struct {
	ctx       context.Context
	cancel    context.CancelFunc
	renderer  mermaid.Renderer
	progress  chan<- ProgressMsg
	workQueue chan WorkItem
	wg        sync.WaitGroup
	mu        sync.Mutex
	rendered  int
	total     int
}

const MaxPrefetchWorkers = 2

// NewPrefetch creates a new prefetch manager with a bounded worker pool.
// The workChan parameter can be used by callers to queue work items.
// The progress channel is used to report completion status back to the TUI model.
func NewPrefetch(ctx context.Context, renderer mermaid.Renderer, progress chan<- ProgressMsg) *Prefetch {
	pctx, cancel := context.WithCancel(ctx)
	p := &Prefetch{
		ctx:       pctx,
		cancel:    cancel,
		renderer:  renderer,
		progress:  progress,
		workQueue: make(chan WorkItem, 100),
		rendered:  0,
		total:     0,
	}

	// Start the worker pool using WaitGroup + semaphore (NOT errgroup.WithContext:
	// one worker failure must not cancel siblings — per CLAUDE.md linters pattern).
	sem := make(chan struct{}, MaxPrefetchWorkers)
	var workerWG sync.WaitGroup
	for range MaxPrefetchWorkers {
		sem <- struct{}{}
		workerWG.Add(1)
		go func() {
			defer func() { <-sem }()
			defer workerWG.Done()
			p.worker(pctx)
		}()
	}

	// Wait for all workers to finish in the background so Close() can join them.
	// Do NOT close p.workQueue: Queue() guards sends with p.ctx.Done(), and
	// closing a channel while Queue() is concurrently selecting would panic.
	p.wg.Go(func() {
		workerWG.Wait()
	})

	return p
}

// worker is the main loop for a prefetch worker.
// It processes work items from the queue until the context is cancelled.
func (p *Prefetch) worker(ctx context.Context) {
	for {
		select {
		case work, ok := <-p.workQueue:
			if !ok {
				return
			}
			p.renderOne(ctx, work)
		case <-ctx.Done():
			return
		}
	}
}

// renderOne attempts to render a single diagram.
// Errors are logged but not propagated to avoid cancelling sibling workers.
// The underlying renderer's cache (if present) handles cache hits.
func (p *Prefetch) renderOne(ctx context.Context, work WorkItem) {
	// Render the diagram
	if p.renderer != nil {
		_, err := p.renderer.Render(ctx, work.Source, work.Theme, work.Width)
		if err != nil && !errors.Is(err, context.Canceled) {
			slog.Debug("prefetch: diagram render failed", "index", work.Index, "error", err)
		}
	}

	p.mu.Lock()
	p.rendered++
	rendered := p.rendered
	total := p.total
	p.mu.Unlock()

	// Report progress (non-blocking)
	select {
	case p.progress <- ProgressMsg{Rendered: rendered, Total: total}:
	case <-ctx.Done():
	}
}

// Queue enqueues work items for rendering.
// Items are prioritized: current file's diagrams first, then others.
// This should be called before any diagrams are actually rendered.
func (p *Prefetch) Queue(items []WorkItem) {
	// Sort items to prioritize current file
	// (items from the same file are grouped, with the current file first)
	sort.Slice(items, func(i, j int) bool {
		// Lower indices come first (current file's diagrams)
		return items[i].Index < items[j].Index
	})

	p.mu.Lock()
	p.rendered = 0
	p.total = len(items)
	p.mu.Unlock()

	// Enqueue all items (non-blocking send with context awareness)
	for _, item := range items {
		select {
		case p.workQueue <- item:
		case <-p.ctx.Done():
			return
		}
	}
}

// Close stops the prefetch manager and waits for all workers to finish.
func (p *Prefetch) Close() {
	p.cancel()
	p.wg.Wait()
}
