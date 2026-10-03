// Package configschema generates and checks the protocol block of a component's configSchema
// from be-protocol schemas/config-keys.yaml, by the profiles the component uses (P2.8, P2.12).
// Gate protocol-config-scan runs Check.
package configschema

import (
	"github.com/brickKit/be-ops/internal/component"
	"github.com/brickKit/be-ops/internal/protocol"
)

// profileOrder is the order profiles are reported in; it follows schemas/conformance-cases.yaml.
var profileOrder = []string{"core", "obs", "err", "auth", "grpc", "events-pub", "events-sub", "db", "jobs", "lifecycle", "blob"}

// Profiles selects the configuration profiles of a component from its manifests, by the rules of
// schemas/conformance-cases.yaml, so the generator and the conformance suite agree:
//
//   - core, obs, err: always;
//   - auth: an OpenAPI operation whose x-be-permission is not `public` (absent counts as guarded),
//     an edge route with auth: required, or an auth key already declared;
//   - grpc: an extra port named grpc;
//   - events-pub: the event contract publishes a subject; events-sub: the fixtures consume one
//     (without a fixtures file: component.yaml already subscribes);
//   - db (and with it jobs, lifecycle): configSchema declares PG_SCHEMA or PG_HOST;
//   - blob: configSchema declares S3_BUCKET or S3_URL.
//
// It also returns the keys with `applies_when` the component opts into: AUTHZ_GRPC_URL when a
// resource type has a share rule (the owner writes tuples), and any such key already declared.
func Profiles(cat *protocol.Catalogue, c *component.Component) (profiles, optIns []string, err error) {
	on := map[string]bool{"core": true, "obs": true, "err": true}
	auth, err := usesAuth(c)
	if err != nil {
		return nil, nil, err
	}
	on["auth"] = auth
	on["grpc"] = c.HasGRPCPort()
	if on["events-pub"], on["events-sub"], err = usesEvents(c); err != nil {
		return nil, nil, err
	}
	on["db"] = declared(c, "PG_SCHEMA", "PG_HOST")
	on["jobs"], on["lifecycle"] = on["db"], on["db"]
	on["blob"] = declared(c, "S3_BUCKET", "S3_URL")
	for _, p := range profileOrder {
		if on[p] {
			profiles = append(profiles, p)
		}
	}
	for _, k := range cat.Keys {
		if k.AppliesWhen == "" {
			continue
		}
		if declared(c, k.Name) || (k.Name == "AUTHZ_GRPC_URL" && ownsShareableType(c)) {
			optIns = append(optIns, k.Name)
		}
	}
	return profiles, optIns, nil
}

func usesAuth(c *component.Component) (bool, error) {
	if declared(c, "AUTHZ_URL", "IAM_URL", "IAM_ISSUER", "TENANT_ID") {
		return true, nil
	}
	for _, r := range c.Assembly.EdgeRoutes {
		if r.Auth == "required" {
			return true, nil
		}
	}
	ops, err := c.OpenAPIOperations()
	if err != nil {
		return false, err
	}
	for _, o := range ops {
		if !o.Internal && o.Permission != "public" {
			return true, nil
		}
	}
	return false, nil
}

func usesEvents(c *component.Component) (pub, sub bool, err error) {
	entries, err := c.EventContracts()
	if err != nil {
		return false, false, err
	}
	for _, e := range entries {
		pub = pub || !e.Signal
	}
	fx, err := c.Fixtures()
	if err != nil {
		return false, false, err
	}
	if fx.Present {
		return pub, len(fx.Consumes) > 0, nil
	}
	return pub, len(c.Events.Subscribes) > 0, nil
}

func declared(c *component.Component, names ...string) bool {
	for _, n := range names {
		if _, ok := c.Prop(n); ok {
			return true
		}
	}
	return false
}

func ownsShareableType(c *component.Component) bool {
	for _, r := range c.Assembly.Resources {
		if r.Share != nil {
			return true
		}
	}
	return false
}
