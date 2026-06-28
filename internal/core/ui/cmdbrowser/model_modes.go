package cmdbrowser

// isPrintable reports whether s is a single visible character that should
// extend the filter query. Multi-byte runes (UTF-8) are allowed; control
// characters (tab, esc, etc.) are not. The filter/inspect state machines that
// once lived here now hang off *browser (plugin.go); this predicate is the only
// survivor — it is consumed by browser.updateFilter.
func isPrintable(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return s != ""
}
