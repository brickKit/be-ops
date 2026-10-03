package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/brickKit/be-ops/internal/authzdecl"
	"github.com/brickKit/be-ops/internal/authzgen"
	"github.com/brickKit/be-ops/internal/component"
)

// loadAll loads the --component directories, else every components/<scope>/<name> that has an
// assembly.yaml (the registries are global, so every installed component counts).
func loadAll(t *targetFlags) ([]*component.Component, error) {
	dirs := t.components
	if len(dirs) == 0 {
		paths, err := filepath.Glob(filepath.Join(*t.root, "components", "*", "*", "assembly.yaml"))
		if err != nil {
			return nil, err
		}
		for _, p := range paths {
			dirs = append(dirs, filepath.Dir(p))
		}
	}
	var cs []*component.Component
	for _, d := range dirs {
		c, err := component.Load(d)
		if err != nil {
			return nil, err
		}
		if c.HasAssembly {
			cs = append(cs, c)
		}
	}
	return cs, nil
}

// writeOrCheck writes content to path, or with check reports whether it differs.
func writeOrCheck(path string, content []byte, check bool) (stale bool, err error) {
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if bytes.Equal(old, content) {
		return false, nil
	}
	if check {
		return true, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	fmt.Println("wrote", path)
	return false, os.WriteFile(path, content, 0o644)
}

// runResources validates the authorization declarations of every component, keeps
// registry/resource-types.tsv (append-only) and writes the RESOURCE_CATALOG file.
func runResources(args []string) error {
	fs := flag.NewFlagSet("resources", flag.ExitOnError)
	t := newTargetFlags(fs)
	catalog := fs.String("catalog", "registry/resource-catalog.json", "RESOURCE_CATALOG file, relative to --root")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cs, err := loadAll(t)
	if err != nil {
		return err
	}
	regPath := filepath.Join(*t.root, "registry", "resource-types.tsv")
	reg, err := authzdecl.ReadTypesTSV(regPath)
	if err != nil {
		return err
	}
	problems, err := authzdecl.Validate(cs, reg)
	if err != nil {
		return err
	}
	rows, warns, err := authzdecl.GenTypes(reg, cs)
	if err != nil {
		problems = append(problems, err.Error())
	}
	for _, w := range warns {
		fmt.Fprintln(os.Stderr, "⚠", w)
	}
	cat, err := authzdecl.Catalog(cs)
	if err != nil {
		return err
	}
	if len(problems) == 0 {
		files := []struct {
			path    string
			content []byte
		}{{regPath, authzdecl.RenderTypesTSV(rows)}, {filepath.Join(*t.root, *catalog), cat}}
		for _, f := range files {
			path := f.path
			stale, err := writeOrCheck(path, f.content, *t.check)
			if err != nil {
				return err
			}
			if stale {
				problems = append(problems, path+" is not current: run be-ops resources")
			}
		}
	}
	return report("resources", problems, len(cs))
}

// runDataSubjects checks registry/data-subjects.tsv against the erasure subjects of every
// component's migrations/lifecycle.yaml.
func runDataSubjects(args []string) error {
	fs := flag.NewFlagSet("data-subjects", flag.ExitOnError)
	t := newTargetFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cs, err := loadAll(t)
	if err != nil {
		return err
	}
	rows, err := authzdecl.ReadSubjectsTSV(filepath.Join(*t.root, "registry", "data-subjects.tsv"))
	if err != nil {
		return err
	}
	problems, err := authzdecl.CheckSubjects(rows, cs)
	if err != nil {
		return err
	}
	return report("data-subjects", problems, len(cs))
}

// runAuthzgen renders each component's authzgen file.
func runAuthzgen(args []string) error {
	fs := flag.NewFlagSet("authzgen", flag.ExitOnError)
	t := newTargetFlags(fs)
	out := fs.String("out", "", "output file (one --component only); its extension picks the language")
	goImport := fs.String("go-sdk-import", "github.com/brickKit/be-sdk-go", "import path of the Go SDK package besdk")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out != "" && len(t.components) != 1 {
		return fmt.Errorf("authzgen: --out needs exactly one --component")
	}
	dirs, err := t.dirs()
	if err != nil {
		return err
	}
	var problems []string
	for _, d := range dirs {
		c, err := component.Load(d)
		if err != nil {
			return err
		}
		if len(c.Assembly.Permissions) == 0 && len(c.Assembly.Resources) == 0 {
			continue
		}
		lang, path, err := target(d, *out)
		if err != nil {
			return err
		}
		src, err := authzgen.Render(c, lang, authzgen.Options{GoSDKImport: *goImport})
		if err != nil {
			return err
		}
		stale, err := writeOrCheck(path, src, *t.check)
		if err != nil {
			return err
		}
		if stale {
			problems = append(problems, fmt.Sprintf("%s: %s is not current: run be-ops authzgen", c.ID, path))
		}
	}
	return report("authzgen", problems, len(dirs))
}

func target(dir, out string) (authzgen.Lang, string, error) {
	if out == "" {
		return authzgen.Detect(dir)
	}
	lang, err := authzgen.LangOf(out)
	return lang, out, err
}

func report(name string, problems []string, n int) error {
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, "✗", p)
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s: %d problem(s)", name, len(problems))
	}
	fmt.Printf("✓ %s: %d component(s) checked\n", name, n)
	return nil
}
