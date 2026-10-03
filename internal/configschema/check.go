package configschema

import (
	"fmt"
	"strings"

	"github.com/brickKit/be-ops/internal/component"
	"github.com/brickKit/be-ops/internal/protocol"
)

// Check compares a component's configSchema with the generated block and validates its own keys
// (P2.4, P2.12). It returns one line per problem; none means current. Formatting and order are
// not compared: only names, type, default, secret, mount and membership in required.
func Check(cat *protocol.Catalogue, c *component.Component, b Block) []string {
	var out []string
	add := func(format string, a ...any) { out = append(out, c.ID+": "+fmt.Sprintf(format, a...)) }
	want := map[string]Prop{}
	for _, p := range b.Props {
		want[p.Name] = p
	}
	wantReq := map[string]bool{}
	for _, r := range b.Required {
		wantReq[r] = true
	}
	for _, p := range b.Props {
		have, ok := c.Prop(p.Name)
		if !ok {
			add("configSchema lacks protocol key %s (profiles %s)", p.Name, strings.Join(b.Profiles, ", "))
			continue
		}
		if d := attrDiff(have, p); d != "" {
			add("configSchema.%s %s", p.Name, d)
		}
	}
	for _, have := range c.Props {
		if r, ok := cat.RetiredKey(have.Name); ok {
			add("configSchema declares retired key %s (replaced by %s: %s)", have.Name, r.ReplacedBy, r.Note)
			continue
		}
		if _, ok := cat.Key(have.Name); ok {
			if _, w := want[have.Name]; !w {
				add("configSchema declares protocol key %s, which no profile of this component needs", have.Name)
			}
			continue
		}
		out = append(out, ownKeyProblems(cat, c.ID, have)...)
	}
	for _, k := range cat.Keys {
		if c.IsRequired(k.Name) != wantReq[k.Name] {
			add("configSchema.required %s %s", map[bool]string{true: "must list", false: "must not list"}[wantReq[k.Name]], k.Name)
		}
	}
	return out
}

func attrDiff(have component.Prop, want Prop) string {
	var d []string
	if have.Type != want.Type {
		d = append(d, fmt.Sprintf("type %q, want %q", have.Type, want.Type))
	}
	if deref(have.Default) != deref(want.Default) || (have.Default == nil) != (want.Default == nil) {
		d = append(d, fmt.Sprintf("default %s, want %s", show(have.Default), show(want.Default)))
	}
	if have.Secret != want.Secret {
		d = append(d, fmt.Sprintf("secret %v, want %v", have.Secret, want.Secret))
	}
	if have.Mount != want.Mount {
		d = append(d, fmt.Sprintf("mount %q, want %q", have.Mount, want.Mount))
	}
	return strings.Join(d, "; ")
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func show(p *string) string {
	if p == nil {
		return "none"
	}
	return fmt.Sprintf("%q", *p)
}

// ownKeyProblems applies the rules every component-defined key follows.
func ownKeyProblems(cat *protocol.Catalogue, id string, p component.Prop) []string {
	var out []string
	add := func(format string, a ...any) { out = append(out, id+": "+fmt.Sprintf(format, a...)) }
	if cat.IsReserved(p.Name) {
		add("configSchema key %s uses a name reserved by the platform (P2.4)", p.Name)
	}
	file := strings.HasSuffix(p.Name, cat.Secrets.Suffix)
	mounted := p.Mount == cat.Secrets.Mount
	if p.Secret != mounted || p.Secret != file {
		add("configSchema key %s: secret: true, mount: file and a name ending in %s go together (P2.12); has secret=%v mount=%q",
			p.Name, cat.Secrets.Suffix, p.Secret, p.Mount)
	}
	if mounted && p.Type != "string" {
		add("configSchema key %s: mount: file needs type string", p.Name)
	}
	if pat, ok := cat.PatternFor(p.Name); ok && p.Type != pat.Type {
		add("configSchema key %s: keys matching %s are type %s", p.Name, pat.Pattern, pat.Type)
	}
	return out
}
