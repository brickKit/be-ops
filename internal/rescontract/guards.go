package rescontract

import (
	"fmt"
	"strings"

	"github.com/brickKit/be-ops/internal/component"
)

// Guards checks that every user-plane operation declares exactly one guard (P6.2): a permission
// key the component declares (page or action; a field key never guards a route), `authenticated`
// or `public`. An operation without x-be-permission fails closed: it is a problem, never
// "guarded by default". Operations marked x-be-internal: true (or tagged provider) are system
// plane traffic, never routed by the edge, and are not checked here.
func Guards(c *component.Component) ([]string, error) {
	ops, err := c.OpenAPIOperations()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, o := range ops {
		if o.Internal {
			continue
		}
		at := fmt.Sprintf("%s: %s %s (%s)", c.ID, strings.ToUpper(o.Method), o.Path, o.File)
		switch o.Permission {
		case "public", "authenticated":
			continue
		case "":
			out = append(out, at+" declares no x-be-permission: an undeclared guard fails closed; declare a permission key, authenticated or public, or x-be-internal: true for system traffic")
			continue
		}
		p, ok := c.Assembly.Permission(o.Permission)
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("%s is guarded by %q, which is not a permission this component declares", at, o.Permission))
		case p.Type == "field":
			out = append(out, fmt.Sprintf("%s is guarded by field key %s; a route's guard is a page or action key", at, o.Permission))
		}
	}
	return out, nil
}
