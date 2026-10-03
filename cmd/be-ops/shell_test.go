package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/brickKit/be-ops/internal/testfixture"
)

// shellRoot is a project with the widget and a shell hosting it; the shell declares protocol in
// its assembly.yaml like every 3.0.0 component.
func shellRoot(t *testing.T, members string) string {
	root := t.TempDir()
	w := testfixture.Copy(t, "widget")
	for _, f := range []string{"component.yaml", "assembly.yaml"} {
		testfixture.Write(t, root, "components/conformance/widget/"+f, testfixture.Read(t, w, f))
	}
	testfixture.Write(t, root, "shell/be/go-test/component.yaml", "apiVersion: brickkit/v1\nkind: Component\nmetadata: {id: be/go-test, version: 1.1.0}\n"+
		"shell:\n  members: ["+members+"]\nconfigSchema:\n  type: object\n  properties: {}\ndeployment:\n  port: 8090\n  stopGracePeriodSeconds: 30\n")
	testfixture.Write(t, root, "shell/be/go-test/assembly.yaml", "id: be/go-test\nprotocol: \"1.0\"\n")
	return root
}

func TestConfigSchema_ShellBlockFromItsMembers(t *testing.T) {
	root := shellRoot(t, "conformance/widget@1.0.0")
	no := false
	dirs, err := (&targetFlags{root: &root, all: &no}).dirs()
	if err != nil || len(dirs) != 2 || !strings.HasSuffix(dirs[1], filepath.Join("shell", "be", "go-test")) {
		t.Fatalf("default dirs must include protocol shells: %v %v", dirs, err)
	}
	if err := runManifestGen("config-schema", []string{"--check", "--root", root}, configGenerator); err == nil {
		t.Fatal("seen red first: the shell's empty block is stale")
	}
	if err := runManifestGen("config-schema", []string{"--root", root}, configGenerator); err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := testfixture.Read(t, root, "shell/be/go-test/component.yaml")
	if !strings.Contains(got, "PG_POOL_MAX: {type: integer, default: 40}") || strings.Contains(got, "PG_SCHEMA") {
		t.Errorf("shell block:\n%s", got)
	}
	if err := runManifestGen("config-schema", []string{"--check", "--root", root}, configGenerator); err != nil {
		t.Fatalf("after generation: %v", err)
	}
}

func TestConfigSchema_ShellWithAMemberNotInTheProjectFails(t *testing.T) {
	root := shellRoot(t, "erp/missing@3.0.0")
	if err := runManifestGen("config-schema", []string{"--check", "--root", root}, configGenerator); err == nil {
		t.Fatal("a member with no source under components/ must fail")
	}
}
