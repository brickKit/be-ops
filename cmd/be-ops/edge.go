package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/brickKit/be-ops/internal/component"
	"github.com/brickKit/be-ops/internal/edge"
	"gopkg.in/yaml.v3"
)

// runEdge writes (or with --check compares) the edge fields of every deploy file, the be-edge
// middleware file, and checks that every OpenAPI path lies under a declared prefix.
func runEdge(args []string) error {
	fs := flag.NewFlagSet("edge", flag.ExitOnError)
	t := newTargetFlags(fs)
	var deploys multiFlag
	fs.Var(&deploys, "deploy", "a deploy file (repeatable); default: deploy.yaml and every deploy.<env>.yaml except deploy.local.yaml")
	settingsPath := fs.String("settings", "infra/edge.yaml", "edge settings, relative to --root")
	mwPath := fs.String("middleware", "infra/traefik/dynamic/edge.yml", "the be-edge middleware file, relative to --root")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, err := edge.LoadSettings(filepath.Join(*t.root, *settingsPath))
	if err != nil {
		return err
	}
	cs, err := edgeComponents(*t.root, t.components)
	if err != nil {
		return err
	}
	if len(deploys) == 0 {
		if deploys, err = defaultDeploys(*t.root); err != nil {
			return err
		}
	}
	var problems []string
	docker := false
	for _, d := range deploys {
		p, isDocker, err := edgeDeploy(d, *t.root, cs, s, *t.check)
		if err != nil {
			return err
		}
		problems = append(problems, p...)
		docker = docker || isDocker
	}
	if docker {
		stale, err := writeOrCheck(filepath.Join(*t.root, *mwPath), edge.MiddlewareFile(s), *t.check)
		if err != nil {
			return err
		}
		if stale {
			problems = append(problems, *mwPath+" is not current: run be-ops edge")
		}
	}
	for _, c := range cs {
		p, err := edge.Coverage(c)
		if err != nil {
			return err
		}
		problems = append(problems, p...)
	}
	return report("edge", problems, len(cs))
}

// edgeComponents loads every local source (components/*/*, shell/*/*) plus --component dirs.
func edgeComponents(root string, extra []string) ([]*component.Component, error) {
	var dirs []string
	for _, pat := range []string{"components/*/*/component.yaml", "shell/*/*/component.yaml"} {
		ps, err := filepath.Glob(filepath.Join(root, pat))
		if err != nil {
			return nil, err
		}
		for _, p := range ps {
			dirs = append(dirs, filepath.Dir(p))
		}
	}
	dirs = append(dirs, extra...)
	var cs []*component.Component
	for _, d := range dirs {
		c, err := component.Load(d)
		if err != nil {
			return nil, err
		}
		cs = append(cs, c)
	}
	return cs, nil
}

func defaultDeploys(root string) ([]string, error) {
	ps, err := filepath.Glob(filepath.Join(root, "deploy*.yaml"))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range ps {
		b := filepath.Base(p)
		if b == "deploy.local.yaml" || (b != "deploy.yaml" && !strings.HasPrefix(b, "deploy.")) {
			continue
		}
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

// edgeDeploy applies or checks one deploy file. Targets come from the local sources; an entry
// pinned to another version (id@version) takes its port from .brickkit/manifests.
func edgeDeploy(path, root string, cs []*component.Component, s edge.Settings, check bool) ([]string, bool, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	ts := map[string]edge.Target{}
	for _, c := range cs {
		ts[c.ID] = edge.TargetOf(c)
	}
	var head struct {
		Target     string `yaml:"target"`
		Components []struct {
			ID      string `yaml:"id"`
			Members []struct {
				ID string `yaml:"id"`
			} `yaml:"members"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(src, &head); err != nil {
		return nil, false, fmt.Errorf("%s: %w", path, err)
	}
	var ids []string
	for _, e := range head.Components {
		ids = append(ids, e.ID)
		for _, m := range e.Members {
			ids = append(ids, m.ID)
		}
	}
	for _, id := range ids {
		addVersioned(ts, root, id)
	}
	if check {
		d, err := edge.Check(src, ts, s)
		for i := range d {
			d[i] = filepath.Base(path) + " " + d[i]
		}
		return d, head.Target != "k8s", err
	}
	out, err := edge.Apply(src, ts, s)
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", path, err)
	}
	if !bytes.Equal(out, src) {
		if err := os.WriteFile(path, out, 0o644); err != nil {
			return nil, false, err
		}
		fmt.Println("wrote", path)
	}
	return nil, head.Target != "k8s", nil
}

func addVersioned(ts map[string]edge.Target, root, id string) {
	bare, ver, ok := strings.Cut(id, "@")
	base, known := ts[bare]
	if !ok || !known {
		return
	}
	raw, err := os.ReadFile(filepath.Join(root, ".brickkit", "manifests", bare, ver, "component.yaml"))
	if err != nil {
		return
	}
	var m struct {
		Deployment struct {
			Port int `yaml:"port"`
		} `yaml:"deployment"`
	}
	if yaml.Unmarshal(raw, &m) == nil && m.Deployment.Port != 0 {
		base.Port = m.Deployment.Port
	}
	ts[id] = base
}
