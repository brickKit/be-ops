package rescontract

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/brickKit/be-ops/internal/component"
	"github.com/brickKit/be-ops/internal/testfixture"
)

func load(t *testing.T, dir string) *component.Component {
	t.Helper()
	c, err := component.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func applyTo(t *testing.T, dir string) string {
	t.Helper()
	c := load(t, dir)
	out, path, err := Apply(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestGenerate_WidgetGetsCheckExplainAndOneShareSetPerShareableType(t *testing.T) {
	r, err := Generate(load(t, testfixture.Copy(t, "widget")))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"  /_authz/check:\n", "  /_authz/explain:\n",
		"  /_shares/conformance.widget.widget/{id}:\n", "  /_shares/conformance.widget.widget/{id}/{share_id}:\n",
		"operationId: authzCheck", "operationId: listSharesWidget", "operationId: createShareWidget", "operationId: deleteShareWidget",
		"x-be-permission: conformance.widget.share", "x-be-permission: authenticated", "maxItems: 500",
	} {
		if !strings.Contains(r, want) {
			t.Errorf("region lacks %q", want)
		}
	}
	if regexp.MustCompile(`name: (type|domain|name)\n\s+in: path`).MatchString(r) {
		t.Errorf("a type, domain or name path parameter is left:\n%s", r)
	}
	for _, gone := range []string{"{domain}", "{name}", "{type}", "$ref", "<the type"} {
		if strings.Contains(r, gone) {
			t.Errorf("region still has %q:\n%s", gone, r)
		}
	}
}

func TestApply_MergesIntoThePublishedFileAndIsIdempotent(t *testing.T) {
	dir := testfixture.Copy(t, "widget")
	path := filepath.Join(dir, "contracts", "widget.openapi.yaml")
	orig := testfixture.Read(t, dir, "contracts/widget.openapi.yaml")
	if p, err := Check(load(t, dir)); err != nil || len(p) == 0 {
		t.Fatalf("seen red first: the published file lacks the resource contract: %v %v", p, err)
	}
	out := applyTo(t, dir)
	head := orig[:strings.Index(orig, "\ncomponents:")]
	if !strings.HasPrefix(out, strings.TrimRight(head, "\n")) {
		t.Error("the hand-written part of the file changed")
	}
	if !strings.HasSuffix(out, orig[strings.Index(orig, "\ncomponents:"):]) {
		t.Error("the components block changed")
	}
	if p, err := Check(load(t, dir)); err != nil || len(p) != 0 {
		t.Fatalf("after apply: %v %v", p, err)
	}
	if again := applyTo(t, dir); again != out {
		t.Error("not idempotent")
	}
	ops, err := load(t, dir).OpenAPIOperations()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, o := range ops {
		if o.Method == "post" && o.Path == "/conformance/widget/_shares/conformance.widget.widget/{id}" && o.Permission == "conformance.widget.share" && o.File == path {
			found = true
		}
	}
	if !found {
		t.Errorf("the merged share operation is not read back: %+v", ops)
	}
}

func TestApply_NoShareRuleNoSharesAndNoResourcesNoRegion(t *testing.T) {
	dir := testfixture.Copy(t, "widget")
	applyTo(t, dir)
	asm := testfixture.Read(t, dir, "assembly.yaml")
	i, j := strings.Index(asm, "    share:\n"), strings.Index(asm, "    fields:\n")
	testfixture.Write(t, dir, "assembly.yaml", asm[:i]+asm[j:])
	if p, _ := Check(load(t, dir)); len(p) == 0 {
		t.Error("a share rule removed: the region is stale")
	}
	if out := applyTo(t, dir); strings.Contains(out, "  /_shares/") || !strings.Contains(out, "  /_authz/check:") {
		t.Errorf("without a share rule: check and explain only:\n%s", out)
	}
	testfixture.Write(t, dir, "assembly.yaml", asm[:strings.Index(asm, "resources:")])
	if out := applyTo(t, dir); strings.Contains(out, "  /_authz/") || strings.Contains(out, Begin) {
		t.Error("without resources the region is removed")
	}
}

func TestCheck_AHandWrittenPathCollidingWithTheContractIsAProblem(t *testing.T) {
	dir := testfixture.Copy(t, "widget")
	src := testfixture.Read(t, dir, "contracts/widget.openapi.yaml")
	testfixture.Write(t, dir, "contracts/widget.openapi.yaml", strings.Replace(src, "paths:\n", "paths:\n  /_authz/check:\n    post: {operationId: mine, x-be-permission: authenticated}\n", 1))
	if _, _, err := Apply(load(t, dir)); err == nil || !strings.Contains(err.Error(), "/_authz/check") {
		t.Errorf("want a collision error, got %v", err)
	}
}

func TestGuards_FailClosed(t *testing.T) {
	dir := testfixture.Copy(t, "widget")
	if p, err := Guards(load(t, dir)); err != nil || len(p) != 0 {
		t.Fatalf("the widget declares every guard: %v %v", p, err)
	}
	src := testfixture.Read(t, dir, "contracts/widget.openapi.yaml")
	edits := map[string]string{
		"missing":     strings.Replace(src, "      x-be-permission: conformance.widget.create\n", "", 1),
		"undeclared":  strings.Replace(src, "x-be-permission: conformance.widget.create", "x-be-permission: conformance.widget.destroy", 1),
		"field key":   strings.Replace(src, "x-be-permission: conformance.widget.create", "x-be-permission: conformance.widget.price.read", 1),
		"placeholder": strings.Replace(src, "x-be-permission: conformance.widget.create", `x-be-permission: "<domain>.<name>.ops"`, 1),
	}
	for name, s := range edits {
		testfixture.Write(t, dir, "contracts/widget.openapi.yaml", s)
		p, err := Guards(load(t, dir))
		if err != nil || len(p) != 1 || !strings.Contains(p[0], "createWidget") && !strings.Contains(p[0], "POST /conformance/widget/widgets") {
			t.Errorf("%s: problems = %v %v", name, p, err)
		}
	}
	internal := strings.Replace(src, "      x-be-permission: conformance.widget.create\n", "      x-be-internal: true\n", 1)
	testfixture.Write(t, dir, "contracts/widget.openapi.yaml", internal)
	if p, _ := Guards(load(t, dir)); len(p) != 0 {
		t.Errorf("an x-be-internal operation needs no user guard: %v", p)
	}
}
