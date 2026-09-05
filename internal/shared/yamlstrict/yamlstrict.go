// Package yamlstrict is the single strict YAML decoder for dwe config files.
//
// yaml.v3's KnownFields(true) reports an unknown key as
// "field defaults not found in type config.DeployConfig" — a Go type name the
// author of a YAML file has no way to act on. Decode rewrites that into an
// *Error naming the file, the line, the field and the fields actually allowed
// at that position, plus a hint that a field the author did not invent may come
// from a newer dwe.
//
// Two decode outcomes deliberately pass through untouched:
//   - io.EOF, which the four strict pipeline loaders rely on to read an
//     all-comment file as absent and keep the built-in default pipeline;
//   - any non-unknown-field error, returned with the file prefixed.
package yamlstrict

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// versionHint is appended once to any message that reports an unknown field.
const versionHint = "(a field you did not invent may come from a newer dwe version — check `dwe version`)"

// unknownFieldRe matches both shapes of yaml.v3's unknown-field report: the
// lines inside a *yaml.TypeError, and the plain error a custom UnmarshalYAML
// raises (yaml.v3 re-raises a non-*TypeError from an unmarshaler verbatim).
var unknownFieldRe = regexp.MustCompile(`^(?:line (\d+): )?field (\S+) not found in type (\S+)$`)

// UnknownField is one rejected key.
type UnknownField struct {
	Line    int      // 0 when yaml reported none
	Field   string   // the rejected key as written
	Type    string   // Go type as yaml names it (reflect.Type.String())
	Allowed []string // sorted yaml keys of Type; nil when Type is not reachable from out
}

// Error reports the unknown fields of one strict decode. Other carries the
// remaining *yaml.TypeError lines verbatim, so a mixed error (an unknown field
// plus a type mismatch) loses nothing.
type Error struct {
	File    string
	Unknown []UnknownField
	Other   []string
	err     error // original *yaml.TypeError / plain error
}

func (e *Error) Error() string {
	var b strings.Builder
	for _, u := range e.Unknown {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(e.prefix(u.Line))
		fmt.Fprintf(&b, "unknown field %q", u.Field)
		if len(u.Allowed) > 0 {
			fmt.Fprintf(&b, " — allowed here: %s", strings.Join(u.Allowed, ", "))
		}
	}
	for _, line := range e.Other {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		if e.File != "" {
			b.WriteString(e.File + ": ")
		}
		b.WriteString(line)
	}
	if len(e.Unknown) > 0 {
		b.WriteByte('\n')
		b.WriteString(versionHint)
	}
	return b.String()
}

// prefix renders "file:line: ", "file: ", "line N: " or nothing, depending on
// what the caller and yaml could supply. A caller with no path (ParseCommandFile
// passes file == "") keeps its own wrap as the file name.
func (e *Error) prefix(line int) string {
	switch {
	case e.File != "" && line > 0:
		return e.File + ":" + strconv.Itoa(line) + ": "
	case e.File != "":
		return e.File + ": "
	case line > 0:
		return "line " + strconv.Itoa(line) + ": "
	default:
		return ""
	}
}

func (e *Error) Unwrap() error { return e.err }

// Decode strictly decodes data into out (a non-nil pointer). Unknown-field
// errors are rewritten into *Error; io.EOF passes through untouched so callers
// keep their "all-comment file is absent" rule; any other error is returned
// prefixed with file.
func Decode(data []byte, out any, file string) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	err := dec.Decode(out)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, io.EOF):
		return err
	}
	if rewritten := rewrite(err, out, file); rewritten != nil {
		return rewritten
	}
	if file == "" {
		return err
	}
	return fmt.Errorf("%s: %w", file, err)
}

// rewrite turns an unknown-field decode error into *Error, or returns nil when
// err carries no unknown field and is better left alone.
func rewrite(err error, out any, file string) *Error {
	if typeErr, ok := errors.AsType[*yaml.TypeError](err); ok {
		res := &Error{File: file, err: err}
		for _, line := range typeErr.Errors {
			if u, ok := parseUnknown(line); ok {
				res.Unknown = append(res.Unknown, u)
				continue
			}
			res.Other = append(res.Other, line)
		}
		if len(res.Unknown) == 0 && len(res.Other) == 0 {
			return nil
		}
		fillAllowed(res.Unknown, out)
		return res
	}
	u, ok := parseUnknown(err.Error())
	if !ok {
		return nil
	}
	res := &Error{File: file, Unknown: []UnknownField{u}, err: err}
	fillAllowed(res.Unknown, out)
	return res
}

func parseUnknown(line string) (UnknownField, bool) {
	m := unknownFieldRe.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return UnknownField{}, false
	}
	u := UnknownField{Field: m[2], Type: m[3]}
	if m[1] != "" {
		u.Line, _ = strconv.Atoi(m[1])
	}
	return u, true
}

// fillAllowed resolves each unknown field's allowed set from the types
// reachable from out. A type yaml named but reflection cannot reach (an
// unexported intermediate shape, say) simply keeps a nil set.
func fillAllowed(unknown []UnknownField, out any) {
	if len(unknown) == 0 || out == nil {
		return
	}
	idx := typeIndex(reflect.TypeOf(out))
	for i := range unknown {
		unknown[i].Allowed = idx[unknown[i].Type]
	}
}

// AllowedFields returns the yaml keys t accepts, sorted. Pointers are followed;
// a non-struct type has no keys. ",inline" struct fields contribute their own
// keys to t's set, exactly as yaml.v3 flattens them. It is exported so the
// hand-maintained allow-lists that compensate for custom UnmarshalYAML
// implementations can be pinned against the reflected truth in a test.
func AllowedFields(t reflect.Type) []string {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return nil
	}
	set := map[string]bool{}
	collectFields(t, set, map[reflect.Type]bool{})
	if len(set) == 0 {
		return nil
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func collectFields(t reflect.Type, set map[string]bool, seen map[reflect.Type]bool) {
	if seen[t] {
		return
	}
	seen[t] = true
	for f := range t.Fields() {
		if f.PkgPath != "" && !f.Anonymous {
			continue // unexported
		}
		name, inline, skip := fieldTag(f)
		if skip {
			continue
		}
		if inline {
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				collectFields(ft, set, seen)
			}
			continue // an inline map accepts any key; it contributes none
		}
		set[name] = true
	}
}

// fieldTag mirrors yaml.v3's getStructInfo: a bare struct tag with no colon is
// read as the yaml tag, "-" drops the field, and an untagged field keys on its
// lower-cased Go name.
func fieldTag(f reflect.StructField) (name string, inline, skip bool) {
	tag := f.Tag.Get("yaml")
	if tag == "" && !strings.Contains(string(f.Tag), ":") {
		tag = string(f.Tag)
	}
	if tag == "-" {
		return "", false, true
	}
	parts := strings.Split(tag, ",")
	for _, flag := range parts[1:] {
		if flag == "inline" {
			inline = true
		}
	}
	name = parts[0]
	if name == "" {
		name = strings.ToLower(f.Name)
	}
	return name, inline, false
}

// typeIndex maps reflect.Type.String() — the name yaml.v3 prints — to the
// allowed key set, for every struct type reachable from t.
func typeIndex(t reflect.Type) map[string][]string {
	idx := map[string][]string{}
	visitType(t, idx, map[reflect.Type]bool{})
	return idx
}

func visitType(t reflect.Type, idx map[string][]string, seen map[reflect.Type]bool) {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || seen[t] {
		return
	}
	seen[t] = true
	switch t.Kind() {
	case reflect.Struct:
		if fields := AllowedFields(t); len(fields) > 0 {
			idx[t.String()] = fields
		}
		for f := range t.Fields() {
			if f.PkgPath != "" && !f.Anonymous {
				continue
			}
			if _, _, skip := fieldTag(f); skip {
				continue
			}
			visitType(f.Type, idx, seen)
		}
	case reflect.Slice, reflect.Array, reflect.Map:
		visitType(t.Elem(), idx, seen)
	}
}
