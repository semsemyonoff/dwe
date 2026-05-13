// Package tpl provides Go template evaluation for info.yml when/value fields.
package tpl

import (
	"fmt"
	"maps"
	"strings"
	"sync"
	"text/template"

	"github.com/go-sprout/sprout"
	"github.com/go-sprout/sprout/registry/conversion"
	"github.com/go-sprout/sprout/registry/filesystem"
	mapsr "github.com/go-sprout/sprout/registry/maps"
	"github.com/go-sprout/sprout/registry/numeric"
	regexpr "github.com/go-sprout/sprout/registry/regexp"
	"github.com/go-sprout/sprout/registry/semver"
	slicesr "github.com/go-sprout/sprout/registry/slices"
	"github.com/go-sprout/sprout/registry/std"
	stringsr "github.com/go-sprout/sprout/registry/strings"
	timer "github.com/go-sprout/sprout/registry/time"
)

// funcMapOnce caches the base sprout-built FuncMap. Building 10 sprout
// registries on every template render is wasteful; once at first call is
// enough. No mutable state — the cached value is read-only once frozen.
var funcMapOnce = sync.OnceValue(buildFuncMap)

// FuncMap returns a per-call shallow copy of the cached base map.
// The clone is mandatory: commandFuncMap (render_command.go) extends the
// returned map with resolve/resolveMap/resolveFile. Without the clone, the
// first command render would permanently leak those entries into base info
// templates and race under -race. A shallow clone of a ~200-entry map is one
// small alloc — negligible vs template parse/execute.
//
// Registers sprout std, strings, numeric, slices, maps, regexp, conversion,
// time, filesystem, semver. Hermetic: no env, no FS reads, no network, no
// crypto/random.
func FuncMap() template.FuncMap {
	return maps.Clone(funcMapOnce())
}

func buildFuncMap() template.FuncMap {
	h := sprout.New()
	if err := h.AddRegistries(
		std.NewRegistry(),
		stringsr.NewRegistry(),
		numeric.NewRegistry(),
		slicesr.NewRegistry(),
		mapsr.NewRegistry(),
		regexpr.NewRegistry(),
		conversion.NewRegistry(),
		timer.NewRegistry(),
		filesystem.NewRegistry(),
		semver.NewRegistry(),
	); err != nil {
		// Registry registration failing at startup is a programmer/dep-version
		// bug, not a runtime condition. Panic per the "panic for bugs" rule.
		panic(fmt.Errorf("tpl: sprout registry registration: %w", err))
	}
	fm := h.Build()
	fm["appURL"] = appURL
	// shuffle is exposed by the strings registry but uses a package-level
	// math/rand.Source seeded from crypto/rand — not goroutine-safe and
	// violates the hermetic/no-random contract. Remove it explicitly.
	delete(fm, "shuffle")
	return fm
}

// appURL builds a URL string from its components, matching the legacy make url macro.
// port is omitted from the output when it equals the scheme's default port (80/443).
// An optional path is appended with a leading slash.
func appURL(host string, port int, useHTTPS bool, pathParts ...string) string {
	scheme := "http"
	defaultPort := 80
	if useHTTPS {
		scheme = "https"
		defaultPort = 443
	}
	if host == "" {
		host = "localhost"
	}
	path := ""
	if len(pathParts) > 0 && pathParts[0] != "" {
		path = "/" + strings.TrimPrefix(pathParts[0], "/")
	}
	if port == 0 || port == defaultPort {
		return fmt.Sprintf("%s://%s%s", scheme, host, path)
	}
	return fmt.Sprintf("%s://%s:%d%s", scheme, host, port, path)
}
