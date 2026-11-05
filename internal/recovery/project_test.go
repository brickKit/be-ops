package recovery

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
}

// A project's databases from brickkit.yaml and config/: $var: references resolved, shells and components without a
// database have no schema, and the evidence store's schema is never among those that go back. The phase is written
// into the shared variables and taken out again, the rest of the file untouched.
func TestTheProjectsDatabases(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "brickkit.yaml", `project: f3-x
components:
  - id: be/go-infra
    version: 3.0.51
    kind: shell
  - id: infra/audit
    version: 3.0.16
  - id: mdm/org
    version: 3.0.16
  - id: infra/print
    version: 3.0.14
`)
	write(t, dir, "config/vars.yaml", "# shared\nPG_DATABASE: be_experiment\nDEPLOYMENT_ID: d1\n")
	write(t, dir, "config/infra-audit.yaml", "PG_DATABASE: $var:PG_DATABASE\nPG_SCHEMA: infra_audit\n")
	write(t, dir, "config/mdm-org.yaml", "PG_DATABASE: $var:PG_DATABASE\nPG_SCHEMA: mdm_org\n")
	write(t, dir, "config/infra-print.yaml", "PRINT_THING: x\n")
	p, err := LoadProject(dir)
	require.NoError(t, err)
	assert.Equal(t, "f3-x", p.Name)
	require.Len(t, p.Components, 4)
	assert.Equal(t, []Component{{ID: "mdm/org", Version: "3.0.16", Database: "be_experiment", Schema: "mdm_org"}}, p.Restored())

	require.NoError(t, p.SetPhase("frozen"))
	require.NoError(t, p.SetPhase("redelivering"))
	raw, _ := os.ReadFile(filepath.Join(dir, "config/vars.yaml"))
	assert.Equal(t, "# shared\nPG_DATABASE: be_experiment\nDEPLOYMENT_ID: d1\nRECOVERY_PHASE: redelivering\n", string(raw))
	require.NoError(t, p.SetPhase(""))
	raw, _ = os.ReadFile(filepath.Join(dir, "config/vars.yaml"))
	assert.Equal(t, "# shared\nPG_DATABASE: be_experiment\nDEPLOYMENT_ID: d1\n", string(raw))
}
