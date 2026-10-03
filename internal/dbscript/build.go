// Package dbscript generates the project's database setup script (be-protocol P10.1, P19.5;
// foundations 03 "Roles"): per component an owner LOGIN role that owns the schema and a runtime
// LOGIN role with DML only through default privileges; per shell a login role granted each
// member's runtime role WITH INHERIT FALSE, SET TRUE (PostgreSQL 16); role-level timeouts on
// every login role as a backstop. Every name comes from the project's configuration
// (PG_OWNER_USER, PG_USER, PG_SCHEMA, PG_DATABASE); nothing is derived from a component ID.
package dbscript

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/brickKit/be-ops/internal/projconf"
	"gopkg.in/yaml.v3"
)

// Secret names a password: the psql variable the script reads and where db-init takes the value
// from ("env:NAME" or "file:PATH"). be-ops never reads a secret value.
type Secret struct {
	Var    string
	Source string
}

// Identity is one component's database identity.
type Identity struct {
	ID, Database, Schema, Owner, Runtime string
	OwnerPw, RuntimePw                   Secret
}

// ShellLogin is one shell's login role and the runtime roles it may switch to.
type ShellLogin struct {
	ID, Database, Login string
	LoginPw             Secret
	Members             []string // runtime roles
}

// Plan is everything the script creates.
type Plan struct {
	Components []Identity
	Shells     []ShellLogin
}

// Build reads the identities of every installed component from its configuration. database,
// when not empty, replaces every PG_DATABASE (a separate test database).
func Build(p *projconf.Project, database string) (Plan, error) {
	var plan Plan
	byID := map[string]Identity{}
	var shells []projconf.Installed
	for _, c := range p.Components {
		if _, isShell := shellMembers(p.Root, c.ID); isShell {
			shells = append(shells, c)
			continue
		}
		id, ok, err := identity(p, c.ID, database)
		if err != nil {
			return Plan{}, err
		}
		if ok {
			plan.Components = append(plan.Components, id)
			byID[c.ID] = id
		}
	}
	if err := distinct(plan.Components); err != nil {
		return Plan{}, err
	}
	for _, s := range shells {
		sl, ok, err := shellLogin(p, s.ID, database, byID)
		if err != nil {
			return Plan{}, err
		}
		if ok {
			plan.Shells = append(plan.Shells, sl)
		}
	}
	return plan, nil
}

func identity(p *projconf.Project, id, database string) (Identity, bool, error) {
	schema, err := p.Resolve(id, "PG_SCHEMA")
	if err != nil || schema.Kind == projconf.Absent {
		return Identity{}, false, err
	}
	out := Identity{ID: id}
	fields := []struct {
		key string
		dst *string
	}{{"PG_SCHEMA", &out.Schema}, {"PG_OWNER_USER", &out.Owner}, {"PG_USER", &out.Runtime}, {"PG_DATABASE", &out.Database}}
	for _, f := range fields {
		if f.key == "PG_DATABASE" && database != "" {
			out.Database = database
			continue
		}
		if *f.dst, err = name(p, id, f.key); err != nil {
			return out, false, err
		}
	}
	if out.OwnerPw, err = secret(p, id, "PG_OWNER_PASSWORD_FILE"); err != nil {
		return out, false, err
	}
	if out.RuntimePw, err = secret(p, id, "PG_PASSWORD_FILE"); err != nil {
		return out, false, err
	}
	if out.Owner == out.Runtime {
		return out, false, fmt.Errorf("%s: PG_OWNER_USER and PG_USER must differ (the runtime role has DML only), both are %q", id, out.Owner)
	}
	if strings.HasSuffix(out.Schema, "_archive") {
		return out, false, fmt.Errorf("%s: PG_SCHEMA %q: there are no _archive schemas any more (cold data lives in the component's bucket)", id, out.Schema)
	}
	return out, true, nil
}

// name resolves a key that must hold a role, schema or database name.
func name(p *projconf.Project, id, key string) (string, error) {
	v, err := p.Resolve(id, key)
	if err != nil {
		return "", err
	}
	if v.Kind != projconf.Literal || v.Value == "" || strings.ContainsRune(v.Value, 0) {
		return "", fmt.Errorf("%s: %s must be a name written in %s (a literal or a $var: reference)", id, key, p.ConfigPath(id))
	}
	return v.Value, nil
}

var nonWord = regexp.MustCompile(`[^a-z0-9]+`)

// secret resolves a password key, which must be ${NAME} or file://path (P2.7).
func secret(p *projconf.Project, id, key string) (Secret, error) {
	v, err := p.Resolve(id, key)
	if err != nil {
		return Secret{}, err
	}
	switch v.Kind {
	case projconf.Env:
		return Secret{Var: v.Value, Source: "env:" + v.Value}, nil
	case projconf.File:
		slug := strings.Trim(nonWord.ReplaceAllString(strings.ToLower(v.Value), "_"), "_")
		return Secret{Var: "pw_file_" + slug, Source: "file:" + v.Value}, nil
	}
	return Secret{}, fmt.Errorf("%s: %s must be ${NAME} or file://path in %s, never a literal", id, key, p.ConfigPath(id))
}

// distinct refuses two components sharing a runtime role, an owner role, or a schema in one
// database, and a role that is an owner in one place and a runtime role in another.
func distinct(ids []Identity) error {
	owner, runtime, schema := map[string]string{}, map[string]string{}, map[string]string{}
	for _, i := range ids {
		if prev, ok := runtime[i.Runtime]; ok {
			return fmt.Errorf("%s and %s share the runtime role %q", prev, i.ID, i.Runtime)
		}
		if prev, ok := owner[i.Owner]; ok {
			return fmt.Errorf("%s and %s share the owner role %q", prev, i.ID, i.Owner)
		}
		key := i.Database + "." + i.Schema
		if prev, ok := schema[key]; ok {
			return fmt.Errorf("%s and %s share the schema %q", prev, i.ID, i.Schema)
		}
		runtime[i.Runtime], owner[i.Owner], schema[key] = i.ID, i.ID, i.ID
	}
	for _, i := range ids {
		if other, ok := owner[i.Runtime]; ok {
			return fmt.Errorf("%s's runtime role %q is %s's owner role", i.ID, i.Runtime, other)
		}
	}
	return nil
}

func shellLogin(p *projconf.Project, id, database string, byID map[string]Identity) (ShellLogin, bool, error) {
	members, _ := shellMembers(p.Root, id)
	sl := ShellLogin{ID: id}
	for _, m := range members {
		if mi, ok := byID[m]; ok {
			sl.Members = append(sl.Members, mi.Runtime)
		}
	}
	if len(sl.Members) == 0 {
		return sl, false, nil
	}
	var err error
	if sl.Login, err = name(p, id, "PG_USER"); err != nil {
		return sl, false, err
	}
	if sl.LoginPw, err = secret(p, id, "PG_PASSWORD_FILE"); err != nil {
		return sl, false, err
	}
	sl.Database = database
	if sl.Database == "" {
		if sl.Database, err = name(p, id, "PG_DATABASE"); err != nil {
			return sl, false, err
		}
	}
	for _, mi := range byID {
		if sl.Login == mi.Owner || sl.Login == mi.Runtime {
			return sl, false, fmt.Errorf("%s: the shell's PG_USER %q is a role of %s; a shell logs in with a role of its own and is never granted an owner", id, sl.Login, mi.ID)
		}
	}
	return sl, true, nil
}

// shellMembers reads shell.members of a shell's local source (shell/<id> or components/<id>).
func shellMembers(root, id string) ([]string, bool) {
	for _, base := range []string{"shell", "components"} {
		raw, err := os.ReadFile(filepath.Join(root, base, id, "component.yaml"))
		if err != nil {
			continue
		}
		var m struct {
			Shell *struct {
				Members []string `yaml:"members"`
			} `yaml:"shell"`
		}
		if yaml.Unmarshal(raw, &m) != nil || m.Shell == nil {
			return nil, false
		}
		var out []string
		for _, s := range m.Shell.Members {
			bare, _, _ := strings.Cut(s, "@")
			out = append(out, strings.TrimSpace(bare))
		}
		return out, true
	}
	return nil, false
}
