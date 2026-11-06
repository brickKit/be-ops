package trust

import (
	"context"
	"crypto"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/brickKit/be-sdk-go/v3/runprofile"
	"gopkg.in/yaml.v3"
)

// Usage is be-ops trust's help.
const Usage = `be-ops trust <command> [flags]   (the deployment trust domain's issuer: be-protocol P12.24-P12.27)

  init [--days 3650]             the deployment's CA: its anchor into the run profile, then its key and certificate
                                 into --dir and its certificate into the trust root (--bundle)
  issue --component ID [--role R] [--out DIR] [--name N] [--days 365]
                                 a service credential (SPIFFE ID and DNS name of the component): its issue record into
                                 the run profile, then <out>/<name>.cert.pem and .key.pem (name: the ID, "/" as "-")
  rotate [--days 3650]           the next CA, signed over by the current one and accepting it; both stay in the trust
                                 root until retire; credentials issued from now on chain to the next
  retire --fingerprint FP        a rotated-away CA out of the trust root after the acceptance window (not a revocation)
  revoke --fingerprint FP [--ca] a service credential (or with --ca a CA, which leaves the trust root at once); the
                                 record waits in --dir/pending when the run profile does not take it (the revocation
                                 stands; flush writes it)
  flush                          the pending revocation records into the run profile
  history                        the trust root and the trust records a recovery would not use

  --project DIR (default .) is the BrickKit project; DEPLOYMENT_ID comes from its config/vars.yaml unless --deployment
  names it (a project being prepared). --dir (default <project>/.secrets/trust) holds the CA's key: never mounted into
  a component, kept outside the recovery group. --bundle (default <project>/.secrets/tls/ca.pem) is the trust root the components read
  (SERVICE_TLS_CA_FILE).
  Environment: RUN_PROFILE_URL, RUN_PROFILE_BUCKET, RUN_PROFILE_REGION, RUN_PROFILE_ACCESS_KEY, RUN_PROFILE_SECRET_KEY.`

// Main runs one command.
func Main(args []string, out io.Writer) error {
	if len(args) == 0 {
		fmt.Fprintln(out, Usage)
		return errors.New("which command?")
	}
	cmd := args[0]
	fs := flag.NewFlagSet("trust "+cmd, flag.ContinueOnError)
	project := fs.String("project", ".", "the BrickKit project")
	dir := fs.String("dir", "", "the issuer's directory (default <project>/.secrets/trust)")
	bundle := fs.String("bundle", "", "the trust root (default <project>/.secrets/tls/ca.pem)")
	days := fs.Int("days", 0, "validity in days (default 3650 for a CA, 365 for a credential)")
	component := fs.String("component", "", "the component (issue)")
	role := fs.String("role", "component", "the role signed in with the credential (issue)")
	outDir := fs.String("out", "", "where the credential goes (issue; default <project>/.secrets/tls)")
	name := fs.String("name", "", "the credential's file name (issue; default the ID, \"/\" as \"-\")")
	fp := fs.String("fingerprint", "", "the certificate's SHA-256 over its DER, hex (retire, revoke)")
	ca := fs.Bool("ca", false, "the fingerprint is a CA's (revoke)")
	deployment := fs.String("deployment", "", "the deployment (default DEPLOYMENT_ID of <project>/config/vars.yaml)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	abs, err := filepath.Abs(*project)
	if err != nil {
		return err
	}
	vars := map[string]string{"DEPLOYMENT_ID": *deployment}
	if *deployment == "" {
		raw, err := os.ReadFile(filepath.Join(abs, "config", "vars.yaml"))
		if err != nil {
			return err
		}
		if err := yaml.Unmarshal(raw, &vars); err != nil {
			return err
		}
	}
	or := func(v, d string) string {
		if v != "" {
			return v
		}
		return d
	}
	i := &Issuer{Deployment: vars["DEPLOYMENT_ID"], Dir: or(*dir, filepath.Join(abs, ".secrets", "trust")),
		Bundle: or(*bundle, filepath.Join(abs, ".secrets", "tls", "ca.pem")), Now: time.Now, Out: out}
	if i.Deployment == "" {
		return errors.New("config/vars.yaml names no DEPLOYMENT_ID")
	}
	if i.Profile, err = store(i); err != nil {
		return err
	}
	caDays, credDays := *days, *days
	if *days == 0 {
		caDays, credDays = 3650, 365
	}
	ctx := context.Background()
	switch cmd {
	case "init":
		return i.Init(ctx, caDays)
	case "issue":
		if *component == "" {
			return errors.New("issue needs --component")
		}
		_, err := i.Issue(ctx, *component, *role, or(*outDir, filepath.Join(abs, ".secrets", "tls")),
			or(*name, strings.ReplaceAll(*component, "/", "-")), credDays)
		return err
	case "rotate":
		return i.Rotate(ctx, caDays)
	case "retire":
		return i.Retire(strings.ToLower(*fp))
	case "revoke":
		if *fp == "" {
			return errors.New("revoke needs --fingerprint")
		}
		return i.Revoke(ctx, *fp, *ca)
	case "flush":
		_, err := i.Flush(ctx)
		return err
	case "history":
		return i.History(ctx)
	}
	return fmt.Errorf("no such command: %s\n%s", cmd, Usage)
}

func store(i *Issuer) (*runprofile.Store, error) {
	env := os.Getenv
	host, _ := os.Hostname()
	return runprofile.New(runprofile.Options{Endpoint: env("RUN_PROFILE_URL"), Region: env("RUN_PROFILE_REGION"),
		Bucket: env("RUN_PROFILE_BUCKET"), AccessKey: env("RUN_PROFILE_ACCESS_KEY"),
		Secret: func() string { return env("RUN_PROFILE_SECRET_KEY") }, Deployment: i.Deployment,
		Writer: runprofile.Writer{Component: Writer, Instance: host}, Identity: bundleIdentity{i}})
}

// bundleIdentity is the run profile's view of the issuer: it signs only with the CA (AppendWith), and the history
// starts from the trust root it writes.
type bundleIdentity struct{ i *Issuer }

func (b bundleIdentity) SigningKey() (crypto.Signer, [][]byte, error) {
	k, c, err := b.i.current()
	if err != nil {
		return nil, nil, err
	}
	return k, [][]byte{c.Raw}, nil
}

func (b bundleIdentity) VerifySigner(chain [][]byte) (string, error) {
	roots, err := b.Roots()
	if err != nil {
		return "", err
	}
	if len(chain) == 0 {
		return "", errors.New("no chain")
	}
	leaf, err := x509.ParseCertificate(chain[0])
	if err != nil {
		return "", err
	}
	pool, inter := x509.NewCertPool(), x509.NewCertPool()
	for _, c := range roots {
		pool.AddCert(c)
	}
	for _, d := range chain[1:] {
		if c, err := x509.ParseCertificate(d); err == nil {
			inter.AddCert(c)
		}
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, Intermediates: inter,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}); err != nil {
		return "", err
	}
	if len(leaf.URIs) > 0 {
		return leaf.URIs[0].String(), nil
	}
	return leaf.Subject.CommonName, nil
}

func (b bundleIdentity) Roots() ([]*x509.Certificate, error) {
	roots, err := readBundle(b.i.Bundle)
	if err == nil && len(roots) == 0 {
		err = errors.New("no trust root")
	}
	return roots, err
}
