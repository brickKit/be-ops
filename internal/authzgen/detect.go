package authzgen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Detect finds a component's language from its build files and returns the default path of the
// generated file: go.mod or backend/go.mod (Go), pyproject.toml or backend/pyproject.toml
// (Python), package.json (TypeScript). Exactly one must be present.
func Detect(dir string) (Lang, string, error) {
	type cand struct {
		lang  Lang
		marks []string
		out   string
	}
	cands := []cand{
		{Go, []string{"go.mod", "backend/go.mod"}, "backend/internal/authzgen/authzgen.go"},
		{Python, []string{"pyproject.toml", "backend/pyproject.toml"}, "backend/app/authzgen.py"},
		{TypeScript, []string{"package.json"}, "src/authzgen.ts"},
	}
	var found []cand
	for _, c := range cands {
		for _, m := range c.marks {
			if _, err := os.Stat(filepath.Join(dir, m)); err == nil {
				found = append(found, c)
				break
			}
		}
	}
	if len(found) != 1 {
		return "", "", fmt.Errorf("%s: cannot tell the language (found %d of go.mod, pyproject.toml, package.json); pass --out", dir, len(found))
	}
	return found[0].lang, filepath.Join(dir, found[0].out), nil
}

// LangOf picks the language from an output file's extension.
func LangOf(path string) (Lang, error) {
	switch {
	case strings.HasSuffix(path, ".go"):
		return Go, nil
	case strings.HasSuffix(path, ".py"):
		return Python, nil
	case strings.HasSuffix(path, ".ts"):
		return TypeScript, nil
	}
	return "", fmt.Errorf("%s: the extension must be .go, .py or .ts", path)
}
