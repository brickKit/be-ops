package authzdecl

import (
	"fmt"
	"regexp"

	"github.com/brickKit/be-ops/internal/component"
)

var ident = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// resource checks one resource type:
//
//   - type matches <domain>.<name>.<aggregate>; table is an identifier;
//   - view_key is required, is an own page or action key and is one of keys (E10);
//   - keys are own page or action keys, at least one;
//   - dimensions are dimensions of the component's data_scopes;
//   - relations grant only keys of the type and include only relations of the type, without a
//     cycle; owned_by is authz or component;
//   - the share key is an own action key stated delegable: false, over relations of the type,
//     to user, role, dept or dept_tree;
//   - fields read and edit own field keys (the only place field keys appear);
//   - inherits come from a registered or declared type, as a relation of this type;
//   - derivation graph requires the graph capability.
func (v *checker) resource(r component.Resource) {
	at := "resource " + r.Type
	if !v.s.ResourceType.MatchString(r.Type) {
		v.add("resource type %q is not <domain>.<name>.<aggregate>", r.Type)
	}
	if !ident.MatchString(r.Table) {
		v.add("%s: table %q is not a table name", at, r.Table)
	}
	keys := map[string]bool{}
	if len(r.Keys) == 0 {
		v.add("%s: keys is empty", at)
	}
	for _, k := range r.Keys {
		v.key(at+" keys", k, "page", "action")
		keys[k] = true
	}
	switch {
	case r.ViewKey == "":
		v.add("%s: view_key is required (the key that decides whether a record is visible at all)", at)
	case !keys[r.ViewKey]:
		v.add("%s: view_key %s is not one of its keys", at, r.ViewKey)
	}
	v.dimensions(at, r.Dimensions)
	v.relations(at, r, keys)
	v.share(at, r)
	v.fields(at, r.Fields)
	v.inherits(at, r)
	switch r.Derivation {
	case "", "direct":
	case "graph":
		if !v.requires("graph") {
			v.add("%s: derivation graph needs requires_capabilities to list graph", at)
		}
	default:
		v.add("%s: derivation %q is direct or graph", at, r.Derivation)
	}
}

func (v *checker) dimensions(at string, dims []string) {
	scoped := map[string]bool{}
	for _, d := range v.c.Assembly.DataScopes() {
		scoped[d.Dimension] = true
	}
	for _, d := range dims {
		if !scoped[d] {
			v.add("%s: dimension %s is not in data_scopes", at, d)
		}
	}
}

func (v *checker) relations(at string, r component.Resource, keys map[string]bool) {
	for _, name := range sortedKeys(r.Relations) {
		rel := r.Relations[name]
		for _, g := range rel.Grants {
			if !keys[g] {
				v.add("%s: relation %s grants %s, which is not one of its keys", at, name, g)
			}
		}
		for _, inc := range rel.Includes {
			if _, ok := r.Relations[inc]; !ok {
				v.add("%s: relation %s includes %s, which is not a relation of the type", at, name, inc)
			}
		}
		if rel.OwnedBy != "" && rel.OwnedBy != "authz" && rel.OwnedBy != "component" {
			v.add("%s: relation %s owned_by %q is authz or component", at, name, rel.OwnedBy)
		}
		if includesCycle(r.Relations, name, map[string]bool{}) {
			v.add("%s: relation %s includes itself through a cycle", at, name)
		}
	}
}

func includesCycle(rels map[string]component.Relation, name string, path map[string]bool) bool {
	if path[name] {
		return true
	}
	path[name] = true
	defer delete(path, name)
	for _, inc := range rels[name].Includes {
		if _, ok := rels[inc]; ok && includesCycle(rels, inc, path) {
			return true
		}
	}
	return false
}

func (v *checker) share(at string, r component.Resource) {
	if r.Share == nil {
		return
	}
	v.key(at+" share", r.Share.Key, "action")
	if p, ok := v.c.Assembly.Permission(r.Share.Key); ok && (p.Delegable == nil || *p.Delegable) {
		v.add("%s: share key %s must be stated delegable: false (a share key never enters a delegation profile)", at, r.Share.Key)
	}
	if len(r.Share.Relations) == 0 || len(r.Share.Subjects) == 0 {
		v.add("%s: share needs relations and subjects", at)
	}
	for _, rel := range r.Share.Relations {
		if _, ok := r.Relations[rel]; !ok {
			v.add("%s: share relation %s is not a relation of the type", at, rel)
		}
	}
	for _, s := range r.Share.Subjects {
		if !shareSubjects[s] {
			v.add("%s: share subject %q is user, role, dept or dept_tree", at, s)
		}
	}
}

func (v *checker) fields(at string, fs []component.FieldSet) {
	sets := map[string]bool{}
	for _, f := range fs {
		where := fmt.Sprintf("%s fields %s", at, f.Set)
		if sets[f.Set] {
			v.add("%s: field set declared twice", where)
		}
		sets[f.Set] = true
		if len(f.Columns) == 0 {
			v.add("%s: columns is empty", where)
		}
		v.key(where+" read", f.Read, "field")
		if f.Edit != "" {
			v.key(where+" edit", f.Edit, "field")
		}
	}
}

func (v *checker) inherits(at string, r component.Resource) {
	for _, in := range r.Inherits {
		if _, ok := v.known[in.From]; !ok {
			v.add("%s: inherits from %s, which no component declares and resource-types.tsv does not list", at, in.From)
		}
		if _, ok := r.Relations[in.As]; !ok {
			v.add("%s: inherits as %s, which is not a relation of the type", at, in.As)
		}
		if in.Via == "" || in.Relation == "" {
			v.add("%s: inherits from %s needs via and relation", at, in.From)
		}
	}
}
