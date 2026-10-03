package main

import (
	"flag"

	"github.com/brickKit/be-ops/internal/component"
	"github.com/brickKit/be-ops/internal/rescontract"
)

// runOpenAPI merges the resource contract (be-protocol openapi/resource-authz.yaml, P6.10) into
// each component's published OpenAPI file, and checks that every user-plane operation declares
// its guard (P6.2; a missing x-be-permission fails closed). With --check it writes nothing.
func runOpenAPI(args []string) error {
	fs := flag.NewFlagSet("openapi", flag.ExitOnError)
	t := newTargetFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
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
		if *t.check {
			p, err := rescontract.Check(c)
			if err != nil {
				return err
			}
			problems = append(problems, p...)
		} else {
			out, path, err := rescontract.Apply(c)
			if err != nil {
				return err
			}
			if path != "" {
				if _, err := writeOrCheck(path, out, false); err != nil {
					return err
				}
				if c, err = component.Load(d); err != nil {
					return err
				}
			}
		}
		g, err := rescontract.Guards(c)
		if err != nil {
			return err
		}
		problems = append(problems, g...)
	}
	return report("openapi", problems, len(dirs))
}
