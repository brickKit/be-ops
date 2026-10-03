// Package rescontract merges the resource contract (be-protocol openapi/resource-authz.yaml,
// P6.10) into a component's published OpenAPI file, and checks that every user-plane operation
// declares its guard (P6.2, fail closed). The contract is read from the pinned be-protocol module,
// never copied: `/{domain}/{name}` becomes the component's prefix, `_shares/{type}` is written
// once per resource type with a share rule (the type's share key guards the writes), and every
// $ref is inlined so the region stands alone. The region sits at the end of the file's `paths:`
// block between two marker comments; every hand-written line stays as it was.
package rescontract

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/brickKit/be-ops/internal/component"
	beprotocol "github.com/brickKit/be-protocol"
	"gopkg.in/yaml.v3"
)

// The marker comments around the generated region.
const (
	Begin = "# >>> be-ops: resource contract from be-protocol openapi/resource-authz.yaml (P6.10); generated, do not edit"
	End   = "# <<< be-ops: resource contract"
)

const fragmentPath = "openapi/resource-authz.yaml"

// problemSchema replaces the fragment's reference to be-protocol's problem schema, which a
// component's file cannot reach.
var problemSchema = map[string]string{"type": "object", "description": "RFC 9457 problem details (be-protocol schemas/problem.schema.json)"}

type share struct{ key, suffix string }

// Generate renders the region for a component (the lines between the markers, indented under
// paths:), with paths relative to prefix (the part of /<id> the file's servers URL does not
// cover). It is empty when the component declares no resources.
func Generate(c *component.Component) (string, error) {
	t, err := target(c)
	if err != nil || t == nil {
		return "", err
	}
	return generate(c, t.prefix)
}

func generate(c *component.Component, prefix string) (string, error) {
	if len(c.Assembly.Resources) == 0 {
		return "", nil
	}
	frag, err := loadFragment()
	if err != nil {
		return "", err
	}
	var shares []share
	for _, r := range c.Assembly.Resources {
		if r.Share != nil {
			shares = append(shares, share{key: r.Share.Key, suffix: r.Type})
		}
	}
	sort.Slice(shares, func(i, j int) bool { return shares[i].suffix < shares[j].suffix })
	out := &yaml.Node{Kind: yaml.MappingNode}
	paths := mapValue(frag, "paths")
	for i := 0; i+1 < len(paths.Content); i += 2 {
		rel := strings.TrimPrefix(paths.Content[i].Value, "/{domain}/{name}")
		item := paths.Content[i+1]
		if !strings.Contains(rel, "{type}") {
			n, err := pathItem(frag, item, nil)
			if err != nil {
				return "", err
			}
			out.Content = append(out.Content, str(prefix+rel), n)
			continue
		}
		for _, s := range shares {
			n, err := pathItem(frag, item, &s)
			if err != nil {
				return "", err
			}
			out.Content = append(out.Content, str(prefix+strings.Replace(rel, "{type}", s.suffix, 1)), n)
		}
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(out); err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n") + "\n", nil
}

func loadFragment() (*yaml.Node, error) {
	raw, err := beprotocol.FS.ReadFile(fragmentPath)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", fragmentPath, err)
	}
	return doc.Content[0], nil
}

// pathItem copies one path item with its references inlined, the domain/name (and for a share
// path the type) parameters dropped, and for a share path the share key and an operationId
// suffix filled in.
func pathItem(frag, item *yaml.Node, s *share) (*yaml.Node, error) {
	n, err := inline(frag, item, 0)
	if err != nil {
		return nil, err
	}
	dropParams(n, s != nil)
	for i := 0; i+1 < len(n.Content); i += 2 {
		op := n.Content[i+1]
		if op.Kind != yaml.MappingNode || n.Content[i].Value == "parameters" {
			continue
		}
		dropParams(op, s != nil)
		if s != nil {
			if id := mapValue(op, "operationId"); id != nil {
				id.Value += pascal(s.suffix[strings.LastIndex(s.suffix, ".")+1:])
			}
		}
		perm := mapValue(op, "x-be-permission")
		if perm != nil && strings.HasPrefix(perm.Value, "<") {
			if s == nil {
				return nil, fmt.Errorf("%s: placeholder %q outside a share path", fragmentPath, perm.Value)
			}
			perm.Value, perm.Style, perm.Tag = s.key, 0, "!!str"
		}
	}
	return n, nil
}

// inline deep-copies n, replacing every $ref: local ones by a copy of their target, other ones by
// problemSchema. Comments and collection styles are dropped so the output has one layout.
func inline(frag, n *yaml.Node, depth int) (*yaml.Node, error) {
	if depth > 32 {
		return nil, fmt.Errorf("%s: $ref nesting too deep", fragmentPath)
	}
	if n.Kind == yaml.MappingNode {
		if ref := mapValue(n, "$ref"); ref != nil {
			if !strings.HasPrefix(ref.Value, "#/") {
				var p yaml.Node
				_ = p.Encode(problemSchema)
				return &p, nil
			}
			t := frag
			for _, seg := range strings.Split(strings.TrimPrefix(ref.Value, "#/"), "/") {
				if t = mapValue(t, seg); t == nil {
					return nil, fmt.Errorf("%s: unresolved $ref %s", fragmentPath, ref.Value)
				}
			}
			return inline(frag, t, depth+1)
		}
	}
	c := &yaml.Node{Kind: n.Kind, Tag: n.Tag, Value: n.Value}
	if n.Kind == yaml.ScalarNode {
		c.Style = n.Style &^ (yaml.FlowStyle | yaml.FoldedStyle)
	}
	for _, ch := range n.Content {
		cc, err := inline(frag, ch, depth+1)
		if err != nil {
			return nil, err
		}
		c.Content = append(c.Content, cc)
	}
	return c, nil
}

// dropParams removes the domain and name path parameters (and type, for a share path) from a
// mapping's parameters list, and the list itself when nothing is left.
func dropParams(n *yaml.Node, dropType bool) {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value != "parameters" {
			continue
		}
		seq := n.Content[i+1]
		var keep []*yaml.Node
		for _, p := range seq.Content {
			name := mapValue(p, "name")
			in := mapValue(p, "in")
			drop := name != nil && in != nil && in.Value == "path" &&
				(name.Value == "domain" || name.Value == "name" || (dropType && name.Value == "type"))
			if !drop {
				keep = append(keep, p)
			}
		}
		if len(keep) == 0 {
			n.Content = append(n.Content[:i], n.Content[i+2:]...)
			return
		}
		seq.Content = keep
		return
	}
}

func pascal(s string) string {
	var b strings.Builder
	for _, part := range strings.Split(s, "_") {
		if part != "" {
			b.WriteString(strings.ToUpper(part[:1]) + part[1:])
		}
	}
	return b.String()
}

func str(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }

func mapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}
