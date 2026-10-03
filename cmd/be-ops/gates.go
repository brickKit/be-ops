package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
)

// gate is one be-acceptance gate (A7) and the be-ops call it makes.
type gate struct {
	name string
	cmd  string   // the subcommand
	args []string // its arguments after the subcommand (--root is appended)
	run  func(args []string) error
}

func gateTable(root, permBase string) []gate {
	cfg := func(a []string) error {
		return runManifestGen("config-schema", a, configGenerator)
	}
	evt := func(a []string) error {
		return runManifestGen("events", a, func(string) (generator, error) { return eventsGen{}, nil })
	}
	perm := []string{"--check", "--root", root}
	if permBase != "" {
		perm = append(perm, "--base", permBase)
	}
	return []gate{
		{"protocol-config-scan", "config-schema", []string{"--check", "--root", root}, cfg},
		{"events-declaration-scan", "events", []string{"--check", "--root", root}, evt},
		{"openapi-fresh", "openapi", []string{"--check", "--root", root}, runOpenAPI},
		{"authzgen-fresh", "authzgen", []string{"--check", "--root", root}, runAuthzgen},
		{"resources-fresh", "resources", []string{"--check", "--root", root}, runResources},
		{"data-subjects-cover", "data-subjects", []string{"--root", root}, runDataSubjects},
		{"permissions-append-only", "permissions", perm, runPermissions},
		{"edge-routes-fresh", "edge", []string{"--check", "--root", root}, runEdge},
		{"registry-check", "registry", []string{"check", "--root", root}, runRegistry},
	}
}

// gates is "gates [--root <path>] [--permissions-base <file>] [--run] [--gate <name>]…": it prints
// "<gate>\t<be-ops command>" for every check the be-acceptance gates make (A7), so the gates and
// be-ops cannot drift apart; with --run it runs the selected ones in this process and fails when
// any fails.
func gates(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("gates", flag.ExitOnError)
	root := fs.String("root", ".", "assembly repository root")
	base := fs.String("permissions-base", "", "the committed registry/permissions.tsv (git show HEAD:registry/permissions.tsv)")
	run := fs.Bool("run", false, "run the checks instead of listing them")
	var only multiFlag
	fs.Var(&only, "gate", "run only this gate (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	table := gateTable(*root, *base)
	sel, err := selectGates(table, only)
	if err != nil {
		return err
	}
	if !*run {
		for _, g := range sel {
			fmt.Fprintf(w, "%s\tbe-ops %s %s\n", g.name, g.cmd, strings.Join(g.args, " "))
		}
		return nil
	}
	var failed []string
	for _, g := range sel {
		if err := g.run(g.args); err != nil {
			fmt.Fprintf(w, "✗ %s: %v\n", g.name, err)
			failed = append(failed, g.name)
			continue
		}
		fmt.Fprintf(w, "✓ %s\n", g.name)
	}
	if len(failed) > 0 {
		return fmt.Errorf("gates: %d of %d failed: %s", len(failed), len(sel), strings.Join(failed, ", "))
	}
	return nil
}

func selectGates(table []gate, only []string) ([]gate, error) {
	if len(only) == 0 {
		return table, nil
	}
	var out []gate
	for _, name := range only {
		found := false
		for _, g := range table {
			if g.name == name {
				out, found = append(out, g), true
			}
		}
		if !found {
			return nil, fmt.Errorf("gates: no gate %q", name)
		}
	}
	return out, nil
}
