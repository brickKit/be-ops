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
	assert.Equal(t, "# shared\nPG_DATABASE: be_experiment\nDEPLOYMENT_ID: d1\nRECOVERY_PHASE: \"\"\n", string(raw),
		"running: an empty value, the components' $var:RECOVERY_PHASE still resolves")
}

// A component whose cached manifest does not declare RECOVERY_PHASE cannot be frozen: the recovery stops it (mode:
// disable in the deploy file) and takes back only the lines it wrote.
func TestComponentsWithoutRecoveryModeAreStopped(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "brickkit.yaml", "project: r\ncomponents:\n  - id: be/go-infra\n    version: 3.0.52\n    kind: shell\n  - id: erp/sales\n    version: 3.0.24\n  - id: infra/print\n    version: 3.0.14\n")
	write(t, dir, ".brickkit/manifests/erp/sales/3.0.24/component.yaml", "configSchema:\n  properties:\n    RECOVERY_PHASE:\n      type: string\n")
	write(t, dir, ".brickkit/manifests/infra/print/3.0.14/component.yaml", "configSchema:\n  properties:\n    PG_HOST:\n      type: string\n")
	deploy := "target: docker\ncomponents:\n  - id: erp/sales\n    expose: true\n  - id: infra/print\n    expose: true\n"
	write(t, dir, "deploy.yaml", deploy)
	p, err := LoadProject(dir)
	require.NoError(t, err)
	assert.Equal(t, []string{"infra/print"}, p.Unsupported("frozen"))
	require.NoError(t, p.Hold(p.Unsupported("frozen"), true))
	require.NoError(t, p.Hold(p.Unsupported("frozen"), true), "again: once")
	raw, _ := os.ReadFile(filepath.Join(dir, "deploy.yaml"))
	assert.Equal(t, "target: docker\ncomponents:\n  - id: erp/sales\n    expose: true\n  - id: infra/print\n    mode: disable  "+holdMark+"\n    expose: true\n", string(raw))
	require.NoError(t, p.Hold(p.Unsupported("frozen"), false))
	raw, _ = os.ReadFile(filepath.Join(dir, "deploy.yaml"))
	assert.Equal(t, deploy, string(raw), "back as it was")
}

// A version whose RECOVERY_PHASE enum does not name a phase cannot run in it; the senders of external commands and
// the members with a database are read from the manifests too.
func TestThePhasesAVersionKnows(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "brickkit.yaml", "project: r\ncomponents:\n  - id: erp/sales\n    version: 3.0.24\n  - id: erp/pay\n    version: 1.0.0\n")
	write(t, dir, "config/erp-sales.yaml", "PG_DATABASE: be\nPG_SCHEMA: erp_sales\n")
	write(t, dir, "config/erp-pay.yaml", "PG_DATABASE: be\nPG_SCHEMA: erp_pay\n")
	write(t, dir, ".brickkit/manifests/erp/sales/3.0.24/component.yaml",
		"configSchema:\n  properties:\n    RECOVERY_PHASE:\n      enum: [frozen, redelivering, replaying]\n")
	write(t, dir, ".brickkit/manifests/erp/pay/1.0.0/component.yaml",
		"configSchema:\n  properties:\n    RECOVERY_PHASE:\n      enum: [frozen, verifying, read_only]\n    EXTERNAL_COMMANDS_QUERY_ONLY:\n      type: boolean\n")
	p, err := LoadProject(dir)
	require.NoError(t, err)
	assert.Empty(t, p.Unsupported("frozen"))
	assert.Equal(t, []string{"erp/sales"}, p.Unsupported("verifying"), "an older version: its enum has no verifying")
	assert.Empty(t, p.Unsupported(""), "running: every version")
	assert.Equal(t, []string{"erp/pay"}, p.Senders())
	assert.Equal(t, []string{"erp/pay"}, p.Checked("verifying"))
	assert.Equal(t, []string{"erp/pay", "erp/sales"}, p.Checked("frozen"))
}
