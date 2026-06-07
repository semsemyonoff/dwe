package mermaid

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestFileCache(t *testing.T) {
	cacheDir := t.TempDir()

	callCount := 0
	underlying := &mockRenderer{
		png: []byte("PNG_DATA"),
		onRender: func() {
			callCount++
		},
	}

	versionFunc := func() string { return "1.0" }
	cache := NewFileCache(cacheDir, 10*1024*1024, underlying, versionFunc)

	ctx := context.Background()
	src := "graph LR"
	theme := ThemeDark
	width := 100

	// First call should hit the renderer.
	png1, err := cache.Render(ctx, src, theme, width)
	if err != nil {
		t.Fatalf("first render failed: %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected 1 call, got %d", callCount)
	}
	if string(png1) != "PNG_DATA" {
		t.Errorf("expected PNG_DATA, got %s", png1)
	}

	// Second call should hit cache.
	png2, err := cache.Render(ctx, src, theme, width)
	if err != nil {
		t.Fatalf("second render failed: %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected still 1 call after cache hit, got %d", callCount)
	}
	if string(png2) != "PNG_DATA" {
		t.Errorf("expected PNG_DATA, got %s", png2)
	}

	// Different width should miss cache.
	_, err = cache.Render(ctx, src, theme, 200)
	if err != nil {
		t.Fatalf("third render failed: %v", err)
	}
	if callCount != 2 {
		t.Errorf("expected 2 calls after width change, got %d", callCount)
	}
}

func TestFileCacheLRUEviction(t *testing.T) {
	cacheDir := t.TempDir()

	// Small cache (fit only 2 entries).
	capBytes := int64(20) // 2 * 8 bytes + margin

	underlying := &mockRenderer{png: []byte("PNG_DATA")} // 8 bytes
	versionFunc := func() string { return "1.0" }
	cache := NewFileCache(cacheDir, capBytes, underlying, versionFunc)

	ctx := context.Background()
	theme := ThemeDark

	// Render three different diagrams.
	_, _ = cache.Render(ctx, "diagram1", theme, 100)
	time.Sleep(10 * time.Millisecond)
	_, _ = cache.Render(ctx, "diagram2", theme, 100)
	time.Sleep(10 * time.Millisecond)
	_, _ = cache.Render(ctx, "diagram3", theme, 100)

	// After rendering 3 diagrams with a cap of 20 bytes (fitting 2 8-byte files),
	// eviction should have removed the oldest file (diagram1).
	entries, _ := os.ReadDir(cacheDir)
	if len(entries) > 2 {
		t.Errorf("eviction should have reduced cache to 2 files, got %d", len(entries))
	}
}

func TestFileCacheKeyVariesWithWidth(t *testing.T) {
	cacheDir := t.TempDir()

	underlying := &mockRenderer{png: []byte("PNG_DATA")}
	versionFunc := func() string { return "1.0" }
	cache := NewFileCache(cacheDir, 10*1024*1024, underlying, versionFunc)

	src := "graph LR"
	theme := ThemeDark

	// Render with width 100.
	key100 := cache.cacheKey(src, theme, 100)

	// Render with width 200.
	key200 := cache.cacheKey(src, theme, 200)

	if key100 == key200 {
		t.Errorf("cache keys should differ for different widths")
	}
}

func TestFileCacheConcurrentMisses(t *testing.T) {
	// Concurrent same-key misses must collapse to ONE underlying render via
	// singleflight. The render holds open (blocks on `release`) so every caller
	// is forced into the dedup window; synctest.Wait returns only once all bubble
	// goroutines are durably blocked — one inside onRender, the rest parked in
	// singleflight — which removes the timing race that previously let a fast
	// runner finish the first render before the others entered (flaky "got 2").
	synctest.Test(t, func(t *testing.T) {
		cacheDir := t.TempDir()

		var renderCount atomic.Int32
		release := make(chan struct{})
		underlying := &mockRenderer{
			png: []byte("PNG_DATA"),
			onRender: func() {
				renderCount.Add(1)
				<-release // keep the single in-flight render open until all callers park
			},
		}

		cache := NewFileCache(cacheDir, 10*1024*1024, underlying, func() string { return "1.0" })

		const n = 3
		done := make(chan error, n)
		for range n {
			go func() {
				_, err := cache.Render(context.Background(), "graph LR", ThemeDark, 100)
				done <- err
			}()
		}

		// All n callers are now durably blocked: one in onRender, the rest waiting
		// on it inside singleflight. Exactly one underlying render may have started.
		synctest.Wait()
		if got := renderCount.Load(); got != 1 {
			t.Errorf("expected 1 underlying render for concurrent misses, got %d", got)
		}

		close(release)
		for range n {
			if err := <-done; err != nil {
				t.Fatalf("concurrent render failed: %v", err)
			}
		}
		if got := renderCount.Load(); got != 1 {
			t.Errorf("render count changed after release: got %d, want 1", got)
		}
	})
}

func TestFileCacheCreatesDirIfMissing(t *testing.T) {
	parentDir := t.TempDir()
	cacheDir := filepath.Join(parentDir, "new", "cache", "dir")

	underlying := &mockRenderer{png: []byte("PNG_DATA")}
	versionFunc := func() string { return "1.0" }
	cache := NewFileCache(cacheDir, 10*1024*1024, underlying, versionFunc)

	ctx := context.Background()
	_, err := cache.Render(ctx, "diagram", ThemeDark, 100)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}

	if _, err := os.Stat(cacheDir); err != nil {
		t.Errorf("cache dir should exist after render: %v", err)
	}
}

type mockRenderer struct {
	png      []byte
	err      error
	onRender func()
}

func (m *mockRenderer) Render(ctx context.Context, src string, theme Theme, width int) ([]byte, error) {
	if m.onRender != nil {
		m.onRender()
	}
	if m.err != nil {
		return nil, m.err
	}
	return m.png, nil
}
