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
	Source     string
	Theme      mermaid.Theme
	Width      int
	Index      int // Diagram index for ordering
	generation int // set by Queue; stale items (wrong generation) don't update the counter
}

// ProgressMsg reports prefetch progress to the TUI. Generation matches the
// value Prefetch.generation had when the work item that produced this
// message was queued — the Model uses it to drop stale messages emitted by
// workers that finished after the user already navigated to a different
// topic (those messages reference counters that no longer apply).
type ProgressMsg struct {
	Rendered   int
	Total      int
	Generation int
}

// Prefetch manages background mermaid rendering with a bounded worker pool.
type Prefetch struct {
	ctx        context.Context
	cancel     context.CancelFunc
	renderer   mermaid.Renderer
	progress   chan<- ProgressMsg
	workQueue  chan WorkItem
	wg         sync.WaitGroup
	mu         sync.Mutex
	generation int // incremented on each Queue call to discard stale renders
	rendered   int
	total      int
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

	// Start the worker pool (NOT errgroup.WithContext: one worker failure must
	// not cancel siblings — per CLAUDE.md linters pattern).
	var workerWG sync.WaitGroup
	for range MaxPrefetchWorkers {
		workerWG.Go(func() { p.worker(pctx) })
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
	stale := work.generation != p.generation
	if !stale {
		p.rendered++
	}
	rendered := p.rendered
	total := p.total
	p.mu.Unlock()

	if stale {
		return
	}

	// Report progress (non-blocking). Generation is the work item's own gen
	// (which equals the current p.generation because stale items returned
	// above), so the receiver can compare against the topic it is currently
	// tracking and discard messages from an older topic.
	select {
	case p.progress <- ProgressMsg{Rendered: rendered, Total: total, Generation: work.generation}:
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
	p.generation++
	gen := p.generation
	p.rendered = 0
	p.total = len(items)
	p.mu.Unlock()

	// Stamp each item with the current generation before enqueuing.
	for i := range items {
		items[i].generation = gen
	}

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

// Generation returns the current queue generation. The Model compares this
// against incoming ProgressMsg to drop stale messages emitted by workers
// that finished after the user navigated to a different topic.
func (p *Prefetch) Generation() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.generation
}

// BeginTopic advances the generation without queuing new work. The TUI
// calls this on every topic transition (including no-diagram and directory
// topics, which never reach Queue) so progress ticks from the previously
// loaded topic are dropped by the Model's ProgressMsg handler. Returns the
// new generation for callers that need to compare later.
func (p *Prefetch) BeginTopic() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.generation++
	p.rendered = 0
	p.total = 0
	return p.generation
}
