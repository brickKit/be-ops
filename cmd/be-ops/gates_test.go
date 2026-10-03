package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/brickKit/be-ops/internal/testfixture"
)

func TestGates_ListsEveryCheckCall(t *testing.T) {
	var out bytes.Buffer
	if err := gates([]string{"--root", "/p", "--permissions-base", "/tmp/base.tsv"}, &out); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{
		"protocol-config-scan\tbe-ops config-schema --check --root /p\n",
		"events-declaration-scan\tbe-ops events --check --root /p\n",
		"openapi-fresh\tbe-ops openapi --check --root /p\n",
		"authzgen-fresh\tbe-ops authzgen --check --root /p\n",
		"resources-fresh\tbe-ops resources --check --root /p\n",
		"data-subjects-cover\tbe-ops data-subjects --root /p\n",
		"permissions-append-only\tbe-ops permissions --check --root /p --base /tmp/base.tsv\n",
		"edge-routes-fresh\tbe-ops edge --check --root /p\n",
		"registry-check\tbe-ops registry check --root /p\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("gates lacks %q:\n%s", want, s)
		}
	}
}

func TestGates_RunFailsOnAStaleGateAndPassesWhenCurrent(t *testing.T) {
	root := t.TempDir()
	w := testfixture.Copy(t, "widget")
	for _, f := range []string{"component.yaml", "assembly.yaml", "contracts/widget.openapi.yaml", "contracts/events/widget.events.json", "conformance/fixtures.yaml"} {
		testfixture.Write(t, root, "components/conformance/widget/"+f, testfixture.Read(t, w, f))
	}
	var out bytes.Buffer
	sel := []string{"--run", "--root", root, "--gate", "protocol-config-scan", "--gate", "events-declaration-scan", "--gate", "openapi-fresh"}
	err := gates(sel, &out)
	if err == nil || !strings.Contains(out.String(), "✗ openapi-fresh") || !strings.Contains(out.String(), "✓ protocol-config-scan") {
		t.Fatalf("the widget's published file lacks the resource contract: err=%v\n%s", err, out.String())
	}
	if err := runOpenAPI([]string{"--root", root}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := gates(sel, &out); err != nil {
		t.Fatalf("all selected gates current: %v\n%s", err, out.String())
	}
	if err := gates([]string{"--run", "--root", root, "--gate", "no-such-gate"}, &out); err == nil {
		t.Error("an unknown gate name is refused")
	}
}
