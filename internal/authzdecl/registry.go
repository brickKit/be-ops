package authzdecl

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/brickKit/be-ops/internal/component"
	"gopkg.in/yaml.v3"
)

// TypeRow is one row of registry/resource-types.tsv (append-only: a type is never removed,
// renamed or given another owner; retire one with the deprecated column).
type TypeRow struct {
	Type       string
	Owner      string
	Table      string
	Deprecated string
}

var typesHeader = "type\towner_component\ttable\tdeprecated"

// GenTypes merges the declared resource types into the registry: existing rows stay in place
// (their table is refreshed), new types are appended in name order. A type no longer declared
// and not deprecated is kept and reported as a warning. A declared type registered to another
// owner is an error.
func GenTypes(existing []TypeRow, cs []*component.Component) ([]TypeRow, []string, error) {
	rows := append([]TypeRow(nil), existing...)
	idx := map[string]int{}
	for i, r := range rows {
		idx[r.Type] = i
	}
	declared := map[string]bool{}
	var added []TypeRow
	for _, c := range cs {
		for _, r := range c.Assembly.Resources {
			declared[r.Type] = true
			i, ok := idx[r.Type]
			if !ok {
				added = append(added, TypeRow{Type: r.Type, Owner: c.ID, Table: r.Table})
				continue
			}
			if rows[i].Owner != c.ID {
				return nil, nil, fmt.Errorf("resource type %s is registered to %s, declared by %s: an owner never changes", r.Type, rows[i].Owner, c.ID)
			}
			rows[i].Table = r.Table
		}
	}
	sort.Slice(added, func(i, j int) bool { return added[i].Type < added[j].Type })
	var warns []string
	for _, r := range rows {
		if !declared[r.Type] && r.Deprecated == "" {
			warns = append(warns, fmt.Sprintf("resource type %s (%s) is registered but no component declares it; mark it deprecated if it is retired", r.Type, r.Owner))
		}
	}
	return append(rows, added...), warns, nil
}

// ReadTypesTSV reads the registry; a missing file is an empty registry.
func ReadTypesTSV(path string) ([]TypeRow, error) {
	lines, err := dataLines(path, 4)
	if err != nil || lines == nil {
		return nil, err
	}
	var out []TypeRow
	for _, c := range lines {
		out = append(out, TypeRow{Type: c[0], Owner: c[1], Table: c[2], Deprecated: c[3]})
	}
	return out, nil
}

// RenderTypesTSV renders the registry file.
func RenderTypesTSV(rows []TypeRow) []byte {
	var b strings.Builder
	b.WriteString(typesHeader + "\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\n", r.Type, r.Owner, r.Table, r.Deprecated)
	}
	return []byte(b.String())
}

// WriteTypesTSV writes the registry file.
func WriteTypesTSV(path string, rows []TypeRow) error {
	return os.WriteFile(path, RenderTypesTSV(rows), 0o644)
}

// SubjectRow is one row of registry/data-subjects.tsv (append-only): a personal-data subject
// kind, the component that owns its master data, and a description (data-lifecycle P16).
type SubjectRow struct {
	Subject     string
	Owner       string
	Description string
}

// ReadSubjectsTSV reads the registry; a missing file is an empty registry.
func ReadSubjectsTSV(path string) ([]SubjectRow, error) {
	lines, err := dataLines(path, 3)
	if err != nil || lines == nil {
		return nil, err
	}
	var out []SubjectRow
	for _, c := range lines {
		out = append(out, SubjectRow{Subject: c[0], Owner: c[1], Description: c[2]})
	}
	return out, nil
}

// CheckSubjects reports duplicate subjects in the registry and every erasure subject of a
// component's migrations/lifecycle.yaml that the registry does not list.
func CheckSubjects(rows []SubjectRow, cs []*component.Component) ([]string, error) {
	var out []string
	reg := map[string]bool{}
	for _, r := range rows {
		if reg[r.Subject] {
			out = append(out, fmt.Sprintf("data-subjects.tsv lists %s twice", r.Subject))
		}
		reg[r.Subject] = true
	}
	for _, c := range cs {
		subs, err := erasureSubjects(c.Dir)
		if err != nil {
			return nil, err
		}
		for _, s := range subs {
			if !reg[s.subject] {
				out = append(out, fmt.Sprintf("%s: migrations/lifecycle.yaml table %s erases subject %s, which registry/data-subjects.tsv does not list", c.ID, s.table, s.subject))
			}
		}
	}
	return out, nil
}

type tableSubject struct{ table, subject string }

func erasureSubjects(dir string) ([]tableSubject, error) {
	p := filepath.Join(dir, "migrations", "lifecycle.yaml")
	raw, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var doc struct {
		Tables map[string]struct {
			Erasure struct {
				Subject string `yaml:"subject"`
			} `yaml:"erasure"`
		} `yaml:"tables"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	var out []tableSubject
	for _, t := range sortedKeys(doc.Tables) {
		if s := doc.Tables[t].Erasure.Subject; s != "" {
			out = append(out, tableSubject{t, s})
		}
	}
	return out, nil
}

// dataLines reads a TSV registry with a header line, returning the data rows split into n
// columns; nil for a missing file.
func dataLines(path string, n int) ([][]string, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out [][]string
	sc := bufio.NewScanner(f)
	first := true
	for sc.Scan() {
		l := sc.Text()
		if first {
			first = false
			continue
		}
		if strings.TrimSpace(l) == "" || strings.HasPrefix(l, "#") {
			continue
		}
		cols := strings.Split(l, "\t")
		for len(cols) < n {
			cols = append(cols, "")
		}
		if len(cols) != n {
			return nil, fmt.Errorf("%s: a row with %d columns, want %d: %q", path, len(cols), n, l)
		}
		out = append(out, cols)
	}
	return out, sc.Err()
}
