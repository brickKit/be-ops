package yamlblock

import (
	"fmt"
	"strings"
)

// ReplaceRegion replaces a generated region inside the top-level block `key:` of a hand-written
// YAML file. The region is the lines between a begin and an end marker comment, indented one
// level (two spaces); it sits at the end of the block, after its last non-blank line. Every
// other line of the file stays as written. region is the text between the markers (each line
// already indented); an empty region removes the markers. A missing block is appended when the
// region is not empty.
func ReplaceRegion(src []byte, key, begin, end, region string) ([]byte, error) {
	lines := strings.Split(strings.TrimSuffix(string(src), "\n"), "\n")
	start := -1
	for i, l := range lines {
		if isKeyLine(l, key) {
			start = i
			break
		}
	}
	var body []string
	if region != "" {
		body = append([]string{"  " + begin}, strings.Split(strings.TrimSuffix(region, "\n"), "\n")...)
		body = append(body, "  "+end)
	}
	if start < 0 {
		if region == "" {
			return src, nil
		}
		out := append([]string{}, lines...)
		if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
			out = append(out, "")
		}
		return join(append(append(out, key+":"), body...)), nil
	}
	if strings.TrimSpace(lines[start][len(key)+1:]) != "" && !strings.HasPrefix(strings.TrimSpace(lines[start][len(key)+1:]), "#") {
		return nil, fmt.Errorf("%s: is written inline; a generated region needs a block mapping", key)
	}
	stop := start + 1
	for stop < len(lines) && continues(lines[stop]) {
		stop++
	}
	b, e := -1, -1
	for i := start + 1; i < stop; i++ {
		switch strings.TrimSpace(lines[i]) {
		case begin:
			b = i
		case end:
			e = i
		}
	}
	if (b < 0) != (e < 0) || e < b {
		return nil, fmt.Errorf("%s: the generated region's markers %q and %q are not a pair", key, begin, end)
	}
	if b >= 0 {
		lines = append(append([]string{}, lines[:b]...), lines[e+1:]...)
		stop -= e + 1 - b
	}
	last := stop
	for last > start+1 && strings.TrimSpace(lines[last-1]) == "" {
		last--
	}
	out := append([]string{}, lines[:last]...)
	out = append(out, body...)
	return join(append(out, lines[last:]...)), nil
}

// Region returns the text between the markers of the generated region inside block `key:`, or ""
// when there is none.
func Region(src []byte, key, begin, end string) string {
	lines := strings.Split(string(src), "\n")
	in, inBlock := false, false
	var out []string
	for _, l := range lines {
		switch {
		case isKeyLine(l, key):
			inBlock = true
			continue
		case inBlock && !continues(l):
			inBlock = false
		}
		if !inBlock {
			continue
		}
		switch strings.TrimSpace(l) {
		case begin:
			in = true
			continue
		case end:
			return strings.Join(out, "\n") + "\n"
		}
		if in {
			out = append(out, l)
		}
	}
	return ""
}
