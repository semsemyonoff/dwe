package mermaid

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// FileCache caches mermaid render results on disk with LRU eviction.
// It wraps another Renderer and deduplicates concurrent same-key misses via singleflight.
type FileCache struct {
	Dir        string
	CapBytes   int64
	underlying Renderer
	Version    func() string

	sf singleflight.Group
	mu sync.Mutex
}

// NewFileCache constructs a FileCache wrapping the given renderer.
// Dir is the cache directory (e.g. $XDG_CACHE_HOME/devbox/mermaid).
// CapBytes is the max cache size; eviction removes oldest by mtime until under cap.
// Version is called once (via sync.OnceValue in callers) to include in the cache key.
func NewFileCache(dir string, capBytes int64, underlying Renderer, version func() string) *FileCache {
	return &FileCache{
		Dir:        dir,
		CapBytes:   capBytes,
		underlying: underlying,
		Version:    version,
	}
}

// Lookup returns the cached PNG bytes for the given source/theme/width
// without falling through to the underlying renderer. The second return is
// false on cache miss. Used by callers (the TUI) that need a synchronous
// cache check without risking a blocking mmdc invocation.
func (fc *FileCache) Lookup(src string, theme Theme, width int) ([]byte, bool) {
	key := fc.cacheKey(src, theme, width)
	keyPath := filepath.Join(fc.Dir, key+".png")
	data, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, false
	}
	t := now()
	_ = os.Chtimes(keyPath, t, t)
	return data, true
}

// Render returns the PNG for the given mermaid source, using the disk cache.
// The cache key is sha256(src + "|" + theme + "|" + width + "|" + version)[:32].
// Concurrent same-key misses are deduplicated via singleflight.
func (fc *FileCache) Render(ctx context.Context, src string, theme Theme, width int) ([]byte, error) {
	key := fc.cacheKey(src, theme, width)
	keyPath := filepath.Join(fc.Dir, key+".png")

	// Try cache hit first (before singleflight so we don't hold the lock).
	if data, err := os.ReadFile(keyPath); err == nil {
		// Refresh mtime to mark as recently used (for LRU eviction).
		t := now()
		_ = os.Chtimes(keyPath, t, t)
		fc.evictIfNeeded()
		return data, nil
	}

	// Cache miss or error reading. Deduplicate concurrent same-key misses.
	result, err, _ := fc.sf.Do(key, func() (any, error) {
		// Render via the underlying renderer.
		png, err := fc.underlying.Render(ctx, src, theme, width)
		if err != nil {
			return nil, err
		}

		// Atomically write to cache: write temp file, then rename.
		if err := fc.writeCached(keyPath, png); err != nil {
			// Write failure doesn't prevent returning the rendered result to the caller.
			// Just log and continue (important for scenarios where cache dir is full).
			return png, nil
		}

		return png, nil
	})

	if err != nil {
		return nil, err
	}

	// Trigger eviction if needed (only do this once, guarded by mu).
	fc.evictIfNeeded()

	if png, ok := result.([]byte); ok {
		return png, nil
	}
	return nil, ErrRenderingDisabled
}

// writeCached atomically writes png to keyPath and triggers eviction if needed.
func (fc *FileCache) writeCached(keyPath string, png []byte) error {
	// Ensure dir exists.
	if err := os.MkdirAll(fc.Dir, 0o700); err != nil {
		return err
	}

	// Write to temp file, then rename (atomic).
	tmpFile := filepath.Join(fc.Dir, "tmp."+randHex()+".png")
	if err := os.WriteFile(tmpFile, png, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmpFile, keyPath); err != nil {
		_ = os.Remove(tmpFile)
		return err
	}
	return nil
}

// evictIfNeeded checks cache size and deletes oldest files until under cap.
func (fc *FileCache) evictIfNeeded() {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	// Walk dir and collect file sizes + mtimes.
	entries, err := os.ReadDir(fc.Dir)
	if err != nil {
		return // Directory may not exist yet.
	}

	type fileInfo struct {
		path  string
		size  int64
		mtime int64
	}

	var files []fileInfo
	var totalSize int64

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(fc.Dir, e.Name())
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		size := info.Size()
		mtime := info.ModTime().Unix()
		files = append(files, fileInfo{path, size, mtime})
		totalSize += size
	}

	// If under cap, nothing to do.
	if totalSize <= fc.CapBytes {
		return
	}

	// Sort by mtime (oldest first).
	sort.Slice(files, func(i, j int) bool {
		return files[i].mtime < files[j].mtime
	})

	// Delete oldest until under cap.
	for _, f := range files {
		if totalSize <= fc.CapBytes {
			break
		}
		_ = os.Remove(f.path)
		totalSize -= f.size
	}
}

func (fc *FileCache) cacheKey(src string, theme Theme, width int) string {
	h := sha256.New()
	// renderOptsVersion participates in the hash so any change to the
	// mmdc invocation (currently the `--scale 3` flag) invalidates old
	// cache entries instead of silently serving stale-format PNGs.
	_, _ = fmt.Fprintf(h, "%s|%s|%d|%s|%s", src, theme, width, fc.Version(), renderOptsVersion)
	return fmt.Sprintf("%x", h.Sum(nil))[:32]
}

func now() time.Time {
	return time.Now()
}

func randHex() string {
	return fmt.Sprintf("%x", rand.Uint64())
}

// CacheDir returns the cache directory path for mermaid diagrams.
// Returns $XDG_CACHE_HOME/devbox/mermaid/ if XDG_CACHE_HOME is set,
// else os.UserCacheDir() + /devbox/mermaid/,
// else os.TempDir() + /devbox-mermaid/.
func CacheDir() (string, error) {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "devbox", "mermaid"), nil
	}

	if userCache, err := os.UserCacheDir(); err == nil {
		return filepath.Join(userCache, "devbox", "mermaid"), nil
	}

	return filepath.Join(os.TempDir(), "devbox-mermaid"), nil
}
