// Package authzdecl validates the authorization declarations of assembly.yaml (permissions with
// type and delegable, resources with view_key, requires_capabilities; be-protocol P6.10,
// assembly-protocol.schema.json, authz-architecture §4.2), keeps registry/resource-types.tsv and
// writes the RESOURCE_CATALOG file contract-infra-authz reads (catalog.schema.json).
package authzdecl

import (
	"fmt"
	"sort"

	"github.com/brickKit/be-ops/internal/component"
	"github.com/brickKit/be-ops/internal/protocol"
)

var permTypes = map[string]bool{"page": true, "action": true, "field": true}
var shareSubjects = map[string]bool{"user": true, "role": true, "dept": true, "dept_tree": true}

// Validate checks every component's declarations, and across components that each resource type
// has one owner, also against the registry (an owner never changes). It returns one line per
// problem.
func Validate(cs []*component.Component, reg []TypeRow) ([]string, error) {
	s, err := protocol.LoadAssemblySchema()
	if err != nil {
		return nil, err
	}
	known := map[string]string{} // type -> owner
	for _, r := range reg {
		known[r.Type] = r.Owner
	}
	declaredBy := map[string]string{}
	var out []string
	for _, c := range cs {
		for _, r := range c.Assembly.Resources {
			if prev, ok := declaredBy[r.Type]; ok && prev != c.ID {
				out = append(out, fmt.Sprintf("resource type %s is declared by both %s and %s: one owner", r.Type, prev, c.ID))
			}
			declaredBy[r.Type] = c.ID
			if owner, ok := known[r.Type]; ok && owner != c.ID {
				out = append(out, fmt.Sprintf("%s: resource type %s is registered to %s in resource-types.tsv; an owner never changes", c.ID, r.Type, owner))
			}
		}
	}
	for t := range declaredBy {
		known[t] = declaredBy[t]
	}
	for _, c := range cs {
		v := &checker{s: s, c: c, known: known}
		v.permissions()
		v.capabilities()
		for _, r := range c.Assembly.Resources {
			v.resource(r)
		}
		out = append(out, v.out...)
	}
	return out, nil
}

type checker struct {
	s     *protocol.AssemblySchema
	c     *component.Component
	known map[string]string
	out   []string
}

func (v *checker) add(format string, a ...any) {
	v.out = append(v.out, v.c.ID+": "+fmt.Sprintf(format, a...))
}

func (v *checker) permissions() {
	for _, p := range v.c.Assembly.Permissions {
		if !v.s.PermKey.MatchString(p.Key) {
			v.add("permission key %q is not a valid key", p.Key)
		}
		if !permTypes[p.Type] {
			v.add("permission %s has type %q; page, action or field", p.Key, p.Type)
		}
		if p.Type == "field" && p.Delegable != nil && *p.Delegable {
			v.add("permission %s: a field key is never delegable", p.Key)
		}
	}
}

func (v *checker) capabilities() {
	seen := map[string]bool{}
	for _, c := range v.c.Assembly.RequiresCapabilities {
		if !v.s.IsCapability(c) {
			v.add("requires_capabilities names %q, which is not a capability (%v)", c, v.s.Capabilities)
		}
		if seen[c] {
			v.add("requires_capabilities lists %s twice", c)
		}
		seen[c] = true
	}
}

// key checks that k is an own permission of one of the allowed types; it returns the type.
func (v *checker) key(where, k string, allowed ...string) {
	p, ok := v.c.Assembly.Permission(k)
	if !ok {
		v.add("%s: %s is not a permission this component declares", where, k)
		return
	}
	for _, a := range allowed {
		if p.Type == a {
			return
		}
	}
	v.add("%s: %s is a %s key; only %v keys belong here (field keys only in fields)", where, k, p.Type, allowed)
}

func (v *checker) requires(cap string) bool {
	for _, c := range v.c.Assembly.RequiresCapabilities {
		if c == cap {
			return true
		}
	}
	return false
}

func sortedKeys[M ~map[string]V, V any](m M) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
