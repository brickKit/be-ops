package main

import (
	"strings"
	"testing"

	"github.com/brickKit/be-ops/internal/testfixture"
)

func TestOpenAPI_CheckFailsUntilTheContractIsMergedAndGuardsFailClosed(t *testing.T) {
	dir := testfixture.Copy(t, "widget")
	if err := runOpenAPI([]string{"--check", "--component", dir}); err == nil {
		t.Fatal("the published widget file lacks the resource contract: --check must fail")
	}
	if err := runOpenAPI([]string{"--component", dir}); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if err := runOpenAPI([]string{"--check", "--component", dir}); err != nil {
		t.Fatalf("after merge: %v", err)
	}
	src := testfixture.Read(t, dir, "contracts/widget.openapi.yaml")
	testfixture.Write(t, dir, "contracts/widget.openapi.yaml", strings.Replace(src, "      x-be-permission: conformance.widget.create\n", "", 1))
	if err := runOpenAPI([]string{"--check", "--component", dir}); err == nil {
		t.Fatal("an operation without x-be-permission must fail")
	}
}
