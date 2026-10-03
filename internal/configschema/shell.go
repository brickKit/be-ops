package configschema

import (
	"fmt"

	"github.com/brickKit/be-ops/internal/component"
	"github.com/brickKit/be-ops/internal/protocol"
)

// shellOwn are the keys a shell reads for itself beyond its core, obs and err keys (P19.3, P19
// "Input"): the addresses the four process-wide resources are built from (token verifier and
// bundle, database pool, bus connection), its own login role, and the physical pool's sizing.
// Every other protocol key is per member and arrives in the member's entry of
// BRICKKIT_SERVED_MEMBERS_CONFIG: schema, owner and migration keys (migrations never run in a
// shell, P19.8), consumer settings, the AUTHZ_GRPC_URL / IAM_GRPC_URL connections, object storage,
// jobs and lifecycle.
var shellOwn = map[string]bool{
	"AUTHZ_URL": true, "IAM_URL": true, "IAM_ISSUER": true, "TENANT_ID": true,
	"PG_HOST": true, "PG_PORT": true, "PG_DATABASE": true, "PG_USER": true, "PG_PASSWORD_FILE": true,
	"PG_POOL_MAX": true, "PG_POOL_MIN_IDLE": true, "PG_CONN_MAX_LIFETIME": true, "PG_CONN_MAX_IDLE_TIME": true,
	"EVENT_BUS_URL": true, "NATS_URL": true,
}

var processProfiles = map[string]bool{"core": true, "obs": true, "err": true}

// GenerateShell computes the protocol block of a shell's own configSchema. Its profiles are the
// union of its members' profiles (core, obs and err always); its keys are the catalogue keys of
// core, obs and err, and the process-wide keys (shellOwn) of the profiles in that union, with
// shell defaults (PG_POOL_MAX 40, the physical pool).
func GenerateShell(cat *protocol.Catalogue, shell *component.Component, members []*component.Component) (Block, error) {
	on := map[string]bool{"core": true, "obs": true, "err": true}
	for _, m := range members {
		ps, _, err := Profiles(cat, m)
		if err != nil {
			return Block{}, fmt.Errorf("%s member %s: %w", shell.ID, m.ID, err)
		}
		for _, p := range ps {
			on[p] = true
		}
	}
	b := Block{Note: "; the members' union (P19.3)"}
	for _, p := range profileOrder {
		if on[p] {
			b.Profiles = append(b.Profiles, p)
		}
	}
	for _, k := range cat.Keys {
		if !inAny(k, processProfiles) && !(shellOwn[k.Name] && inAny(k, on)) {
			continue
		}
		b.Props = append(b.Props, propOf(k, true))
		if k.Required && len(k.OneOf) == 0 {
			b.Required = append(b.Required, k.Name)
		}
	}
	return b, nil
}

// CheckShell is Check plus the rule that ties a shell to its members: its stopGracePeriodSeconds
// is at least the largest of any member it hosts (P19.9).
func CheckShell(cat *protocol.Catalogue, shell *component.Component, members []*component.Component, b Block) []string {
	out := Check(cat, shell, b)
	for _, m := range members {
		if m.StopGrace > shell.StopGrace {
			out = append(out, fmt.Sprintf("%s: deployment.stopGracePeriodSeconds %d is below member %s's %d (P19.9)",
				shell.ID, shell.StopGrace, m.ID, m.StopGrace))
		}
	}
	return out
}
