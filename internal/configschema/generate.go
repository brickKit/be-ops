package configschema

import (
	"github.com/brickKit/be-ops/internal/component"
	"github.com/brickKit/be-ops/internal/protocol"
)

// Prop is one generated protocol key of configSchema.properties.
type Prop struct {
	Name    string
	Type    string
	Default *string
	Secret  bool
	Mount   string
	Comment string // a line comment for keys whose absence has a meaning
}

// Block is the generated protocol block of one component.
type Block struct {
	Profiles []string
	OptIns   []string
	Props    []Prop
	Required []string // the protocol keys configSchema.required must list
	Note     string   // appended to the marker's profile list (a shell: whose union it is)
}

// Generate computes the protocol block: every catalogue key of a selected profile or opted
// into, in catalogue order, with the catalogue's type, default, secret and mount (P2.8).
func Generate(cat *protocol.Catalogue, c *component.Component) (Block, error) {
	profiles, optIns, err := Profiles(cat, c)
	if err != nil {
		return Block{}, err
	}
	b := Block{Profiles: profiles, OptIns: optIns}
	sel := map[string]bool{}
	for _, p := range profiles {
		sel[p] = true
	}
	opt := map[string]bool{}
	for _, k := range optIns {
		opt[k] = true
	}
	for _, k := range cat.Keys {
		if !opt[k.Name] && !inAny(k, sel) {
			continue
		}
		b.Props = append(b.Props, propOf(k, c.IsShell))
		if k.Required && len(k.OneOf) == 0 {
			b.Required = append(b.Required, k.Name)
		}
	}
	return b, nil
}

func inAny(k protocol.Key, sel map[string]bool) bool {
	for _, p := range k.Profiles {
		if sel[p] {
			return true
		}
	}
	return false
}

func propOf(k protocol.Key, shell bool) Prop {
	p := Prop{Name: k.Name, Type: k.Type, Default: k.Default, Secret: k.Secret, Mount: k.Mount}
	if shell && k.ShellDefault != nil {
		p.Default = k.ShellDefault
	}
	switch {
	case k.DefaultFrom != "" && len(k.OneOf) > 0:
		p.Comment = "one of " + join(k.OneOf) + "; absent = " + k.DefaultFrom
	case len(k.OneOf) > 0:
		p.Comment = "one of " + join(k.OneOf)
	case k.DefaultFrom != "":
		p.Comment = "absent = " + k.DefaultFrom
	}
	return p
}

func join(xs []string) string {
	s := ""
	for i, x := range xs {
		if i > 0 {
			s += ", "
		}
		s += x
	}
	return s
}

// isCatalogueKey reports whether a name is a protocol key or a retired one: such keys are owned
// by the generator and never written by hand.
func isCatalogueKey(cat *protocol.Catalogue, name string) bool {
	if _, ok := cat.Key(name); ok {
		return true
	}
	_, ok := cat.RetiredKey(name)
	return ok
}
