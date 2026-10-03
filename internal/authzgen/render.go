package authzgen

import (
	"fmt"
	"go/format"
	"strconv"
	"strings"

	"github.com/brickKit/be-ops/internal/component"
)

func renderGo(c *component.Component, o Options) ([]byte, error) {
	consts, cat, err := model(c, GoName)
	if err != nil {
		return nil, err
	}
	imp := o.GoSDKImport
	if imp == "" {
		imp = "github.com/brickKit/be-sdk-go"
	}
	js, err := catalogueJSON(cat, true)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "// %s\n\n", header)
	fmt.Fprintf(&b, "// Package authzgen holds the permission keys and resource types of %s as typed constants,\n", c.ID)
	b.WriteString("// and its authorization catalogue for the runtime.\n")
	b.WriteString("package authzgen\n\n")
	fmt.Fprintf(&b, "import besdk %q\n\n", imp)
	writeGoConsts(&b, "Permission keys.", "besdk.PermKey", consts, "key")
	writeGoConsts(&b, "Resource types.", "besdk.ResourceType", consts, "type")
	b.WriteString("// CatalogJSON is the component's authorization catalogue: its keys (type, delegable) and its\n")
	b.WriteString("// resource types (contract-infra-authz schemas/catalog.schema.json, resource_types).\n")
	fmt.Fprintf(&b, "const CatalogJSON = %s\n", goString(js))
	out, err := format.Source([]byte(b.String()))
	if err != nil {
		return nil, fmt.Errorf("%s: generated Go does not format: %w", c.ID, err)
	}
	return out, nil
}

func writeGoConsts(b *strings.Builder, title, typ string, consts []constant, kind string) {
	var any bool
	for _, k := range consts {
		any = any || k.kind == kind
	}
	if !any {
		return
	}
	fmt.Fprintf(b, "// %s\nconst (\n", title)
	for _, k := range consts {
		if k.kind == kind {
			fmt.Fprintf(b, "\t%s %s = %q // %s\n", k.name, typ, k.value, oneLine(k.comment))
		}
	}
	b.WriteString(")\n\n")
}

func goString(s string) string {
	if strings.Contains(s, "`") {
		return strconv.Quote(s)
	}
	return "`" + s + "`"
}

func oneLine(s string) string { return strings.ReplaceAll(s, "\n", " ") }

func renderPython(c *component.Component) ([]byte, error) {
	consts, cat, err := model(c, PyName)
	if err != nil {
		return nil, err
	}
	js, err := catalogueJSON(cat, false)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", header)
	fmt.Fprintf(&b, "\"\"\"Permission keys and resource types of %s, and its authorization catalogue.\"\"\"\n\n", c.ID)
	b.WriteString("from typing import Final\n\n")
	for _, kind := range []string{"key", "type"} {
		for _, k := range consts {
			if k.kind == kind {
				fmt.Fprintf(&b, "%s: Final = %s  # %s\n", k.name, strconv.Quote(k.value), oneLine(k.comment))
			}
		}
	}
	b.WriteString("\n# The component's authorization catalogue (contract-infra-authz schemas/catalog.schema.json).\n")
	fmt.Fprintf(&b, "CATALOG_JSON: Final = %s\n", strconv.Quote(js))
	return []byte(b.String()), nil
}

func renderTS(c *component.Component) ([]byte, error) {
	consts, cat, err := model(c, GoName)
	if err != nil {
		return nil, err
	}
	js, err := catalogueJSON(cat, true)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "// %s\n", header)
	fmt.Fprintf(&b, "// Permission keys and resource types of %s, and its authorization catalogue.\n\n", c.ID)
	for _, kind := range []string{"key", "type"} {
		for _, k := range consts {
			if k.kind == kind {
				fmt.Fprintf(&b, "export const %s = %s as const; // %s\n", k.name, strconv.Quote(k.value), oneLine(k.comment))
			}
		}
	}
	b.WriteString("\n// The component's authorization catalogue (contract-infra-authz schemas/catalog.schema.json).\n")
	fmt.Fprintf(&b, "export const catalog = %s as const;\n", js)
	return []byte(b.String()), nil
}
