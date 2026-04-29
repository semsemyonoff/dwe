// Package tpl provides Go template evaluation for info.yml when/value fields.
package tpl

import (
	"fmt"
	"path/filepath"
	"strings"
	"text/template"
	"time"
)

// nowFn is an injectable function for current time (for testing).
var nowFn = time.Now

// FuncMap returns the template functions available in info.yml expressions.
//
// Available functions:
//
//	appURL host port useHTTPS [path] — builds a URL, omitting the port when it
//	    matches the scheme default (80 for http, 443 for https).
//	date — returns local current date as YYYY-MM-DD.
//	datetime — returns local current date and time as YYYY-MM-DD_HH-MM-SS.
//	base path — returns filepath.Base(path) (OS-aware path separator).
//	dir path — returns filepath.Dir(path) (OS-aware path separator).
func FuncMap() template.FuncMap {
	return template.FuncMap{
		"appURL":   appURL,
		"date":     dateFunc,
		"datetime": datetimeFunc,
		"base":     filepath.Base,
		"dir":      filepath.Dir,
	}
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

// dateFunc returns the current local date in YYYY-MM-DD format.
func dateFunc() string {
	return nowFn().Format("2006-01-02")
}

// datetimeFunc returns the current local date and time in YYYY-MM-DD_HH-MM-SS format.
func datetimeFunc() string {
	return nowFn().Format("2006-01-02_15-04-05")
}
