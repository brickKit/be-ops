package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/brickKit/be-ops/internal/protocol"
	"github.com/brickKit/be-ops/internal/testfixture"
)

func configGenerator(root string) (generator, error) {
	cat, err := protocol.LoadCatalogue()
	return configGen{cat: cat, root: root}, err
}

func TestGates_WidgetIsCurrentAndAStaleCopyFails(t *testing.T) {
	dir := testfixture.Copy(t, "widget")
	if err := runManifestGen("config-schema", []string{"--check", "--component", dir}, configGenerator); err != nil {
		t.Errorf("config-schema --check on the widget: %v", err)
	}
	if err := runManifestGen("events", []string{"--check", "--component", dir}, func(string) (generator, error) { return eventsGen{}, nil }); err != nil {
		t.Errorf("events --check on the widget: %v", err)
	}
	src := testfixture.Read(t, dir, "component.yaml")
	testfixture.Write(t, dir, "component.yaml", strings.Replace(src, "    - conformance.widget.reverted.v1\n", "", 1))
	if err := runManifestGen("events", []string{"--check", "--component", dir}, func(string) (generator, error) { return eventsGen{}, nil }); err == nil {
		t.Error("a stale events block must fail the check")
	}
	if err := runManifestGen("events", []string{"--component", dir}, func(string) (generator, error) { return eventsGen{}, nil }); err != nil {
		t.Errorf("generation repairs it: %v", err)
	}
}

func TestDirs_DefaultSelectsProtocolComponents(t *testing.T) {
	root := t.TempDir()
	w := testfixture.Copy(t, "widget")
	for _, f := range []string{"component.yaml", "assembly.yaml"} {
		testfixture.Write(t, root, "components/conformance/widget/"+f, testfixture.Read(t, w, f))
	}
	testfixture.Write(t, root, "components/erp/old/component.yaml", "metadata: {id: erp/old, version: 2.0.0}\n")
	testfixture.Write(t, root, "components/erp/old/assembly.yaml", "permissions: []\n")
	all, no := true, false
	tf := &targetFlags{root: &root, all: &no}
	dirs, err := tf.dirs()
	if err != nil || len(dirs) != 1 || !strings.HasSuffix(dirs[0], filepath.Join("conformance", "widget")) {
		t.Errorf("default dirs = %v %v", dirs, err)
	}
	tf.all = &all
	if dirs, _ := tf.dirs(); len(dirs) != 2 {
		t.Errorf("--all dirs = %v", dirs)
	}
}

func TestDefaultDeploys_SkipsThePersonalFile(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"deploy.yaml", "deploy.prod.yaml", "deploy.local.yaml", "deployment-notes.yaml"} {
		testfixture.Write(t, root, f, "target: docker\n")
	}
	got, err := defaultDeploys(root)
	if err != nil || len(got) != 2 || filepath.Base(got[0]) != "deploy.prod.yaml" || filepath.Base(got[1]) != "deploy.yaml" {
		t.Errorf("deploy files = %v %v", got, err)
	}
}
