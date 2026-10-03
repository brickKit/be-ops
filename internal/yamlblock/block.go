// Package yamlblock replaces one top-level block of a YAML file as text, so a generator owns
// exactly that block and every other line of a hand-written file (comments, blank lines, key
// order) stays as it was.
package yamlblock

import "strings"

// ReplaceTopLevel replaces the top-level block `key:` with block (which starts with "key:" and
// ends with a newline). The block runs from the key's line to the last non-blank line before the
// next line that starts in column 0 with anything else (another key or a comment heading it).
// An empty block removes the key; a missing key is appended at the end after one blank line.
func ReplaceTopLevel(src []byte, key, block string) []byte {
	lines := strings.Split(string(src), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	start := -1
	for i, l := range lines {
		if isKeyLine(l, key) {
			start = i
			break
		}
	}
	var blockLines []string
	if block != "" {
		blockLines = strings.Split(strings.TrimSuffix(block, "\n"), "\n")
	}
	if start < 0 {
		if block == "" {
			return src
		}
		out := append([]string{}, lines...)
		if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
			out = append(out, "")
		}
		return join(append(out, blockLines...))
	}
	end := start + 1
	for end < len(lines) && continues(lines[end]) {
		end++
	}
	for end > start+1 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	out := append([]string{}, lines[:start]...)
	out = append(out, blockLines...)
	rest := lines[end:]
	if block == "" && len(rest) > 0 && strings.TrimSpace(rest[0]) == "" &&
		(len(out) == 0 || strings.TrimSpace(out[len(out)-1]) == "") {
		rest = rest[1:]
	}
	return join(append(out, rest...))
}

func isKeyLine(l, key string) bool {
	if !strings.HasPrefix(l, key+":") {
		return false
	}
	rest := l[len(key)+1:]
	return rest == "" || rest[0] == ' ' || rest[0] == '\t'
}

// continues reports whether a line belongs to the block above it: blank, indented, or a
// column-0 sequence item.
func continues(l string) bool {
	if strings.TrimSpace(l) == "" {
		return true
	}
	return l[0] == ' ' || l[0] == '\t' || strings.HasPrefix(l, "- ") || l == "-"
}

func join(lines []string) []byte {
	return []byte(strings.Join(lines, "\n") + "\n")
}
