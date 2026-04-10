// Package tpl provides Go template evaluation for help.yml when/value fields.
package tpl

import (
	"fmt"
	"strings"
	"text/template"
)

// FuncMap returns the template functions available in help.yml expressions.
//
// Available functions:
//
//	appURL host port useHTTPS [path] — builds a URL, omitting the port when it
//	    matches the scheme default (80 for http, 443 for https).
func FuncMap() template.FuncMap {
	return template.FuncMap{
		"appURL": appURL,
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
