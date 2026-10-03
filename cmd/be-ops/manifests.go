package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/brickKit/be-ops/internal/component"
	"github.com/brickKit/be-ops/internal/configschema"
	"github.com/brickKit/be-ops/internal/eventsdecl"
	"github.com/brickKit/be-ops/internal/protocol"
)

// multiFlag is a repeatable string flag.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// targetFlags are the flags shared by the generators that rewrite component.yaml.
type targetFlags struct {
	root       *string
	components multiFlag
	all        *bool
	check      *bool
}

func newTargetFlags(fs *flag.FlagSet) *targetFlags {
	t := &targetFlags{
		root:  fs.String("root", ".", "assembly repository root"),
		all:   fs.Bool("all", false, "also components that do not declare protocol in assembly.yaml (2.x)"),
		check: fs.Bool("check", false, "write nothing; exit 1 when a component's block is not current"),
	}
	fs.Var(&t.components, "component", "a component directory (repeatable); default: every components/<scope>/<name>")
	return t
}

// dirs lists the component directories to work on: the --component ones, else every
// components/<scope>/<name> that declares `protocol` in assembly.yaml (3.0.0 and later), or all
// of them with --all.
func (t *targetFlags) dirs() ([]string, error) {
	if len(t.components) > 0 {
		return t.components, nil
	}
	paths, err := filepath.Glob(filepath.Join(*t.root, "components", "*", "*", "component.yaml"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	var out []string
	for _, p := range paths {
		dir := filepath.Dir(p)
		if !*t.all {
			c, err := component.Load(dir)
			if err != nil {
				return nil, err
			}
			if c.Assembly.Protocol == "" {
				continue
			}
		}
		out = append(out, dir)
	}
	return out, nil
}

// generator is one component.yaml block generator: Check returns the problems; Apply the new
// file content.
type generator interface {
	check(c *component.Component) ([]string, error)
	apply(c *component.Component) ([]byte, error)
}

type configGen struct{ cat *protocol.Catalogue }

func (g configGen) check(c *component.Component) ([]string, error) {
	b, err := configschema.Generate(g.cat, c)
	if err != nil {
		return nil, err
	}
	return configschema.Check(g.cat, c, b), nil
}

func (g configGen) apply(c *component.Component) ([]byte, error) {
	b, err := configschema.Generate(g.cat, c)
	if err != nil {
		return nil, err
	}
	return configschema.Apply(c, b)
}

type eventsGen struct{}

func (eventsGen) check(c *component.Component) ([]string, error) {
	d, err := eventsdecl.Generate(c)
	if err != nil {
		return nil, err
	}
	for _, w := range d.Warnings {
		fmt.Fprintln(os.Stderr, "⚠", w)
	}
	return eventsdecl.Check(c, d), nil
}

func (eventsGen) apply(c *component.Component) ([]byte, error) {
	d, err := eventsdecl.Generate(c)
	if err != nil {
		return nil, err
	}
	return eventsdecl.Apply(c.ManifestRaw, d), nil
}

// runManifestGen runs one generator over the target components. With --check it only reports;
// otherwise it rewrites component.yaml and reports what still fails afterwards (problems the
// generator cannot fix, such as a component's own key breaking P2.12).
func runManifestGen(name string, args []string, g func() (generator, error)) error {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	t := newTargetFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	gen, err := g()
	if err != nil {
		return err
	}
	dirs, err := t.dirs()
	if err != nil {
		return err
	}
	var problems []string
	for _, dir := range dirs {
		p, err := runOne(gen, dir, *t.check)
		if err != nil {
			return err
		}
		problems = append(problems, p...)
	}
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, "✗", p)
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s: %d problem(s) in %d component(s)", name, len(problems), len(dirs))
	}
	fmt.Printf("✓ %s: %d component(s) current\n", name, len(dirs))
	return nil
}

func runOne(gen generator, dir string, checkOnly bool) ([]string, error) {
	c, err := component.Load(dir)
	if err != nil {
		return nil, err
	}
	if !checkOnly {
		out, err := gen.apply(c)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(out, c.ManifestRaw) {
			if err := os.WriteFile(c.ManifestPath, out, 0o644); err != nil {
				return nil, err
			}
			fmt.Println("wrote", c.ManifestPath)
		}
		if c, err = component.Load(dir); err != nil {
			return nil, err
		}
	}
	return gen.check(c)
}
