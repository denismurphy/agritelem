// Command devtoken creates a local Ed25519 key pair and issues JWTs for
// development. Production tokens come from an identity provider.
//
//	devtoken keys  -dir ../.dev
//	devtoken issue -key ../.dev/jwt-private.pem -sub ie.example.gateway \
//	    -scopes observations:write,animals:read -location dk.herdnumber/123456
package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/denismurphy/agritelem/backend/internal/auth"
	"github.com/denismurphy/agritelem/backend/internal/domain"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "devtoken:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: devtoken keys|issue [flags]")
	}
	switch args[0] {
	case "keys":
		return keys(args[1:])
	case "issue":
		return issue(args[1:])
	default:
		return fmt.Errorf("unknown command %q; want keys or issue", args[0])
	}
}

func keys(args []string) error {
	fl := flag.NewFlagSet("keys", flag.ContinueOnError)
	dir := fl.String("dir", "../.dev", "directory for jwt-private.pem and jwt-public.pem")
	if err := fl.Parse(args); err != nil {
		return err
	}
	privPath, pubPath := filepath.Join(*dir, "jwt-private.pem"), filepath.Join(*dir, "jwt-public.pem")
	if _, err := os.Stat(privPath); err == nil {
		fmt.Fprintln(os.Stderr, "keys already exist in", *dir)
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	priv, pub, err := auth.GenerateKeyPEM()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*dir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(privPath, priv, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(pubPath, pub, 0o644); err != nil { //nolint:gosec // public key
		return err
	}
	fmt.Fprintln(os.Stderr, "wrote", privPath, "and", pubPath)
	return nil
}

func issue(args []string) error {
	fl := flag.NewFlagSet("issue", flag.ContinueOnError)
	keyPath := fl.String("key", "../.dev/jwt-private.pem", "Ed25519 private key (PKCS #8 PEM)")
	sub := fl.String("sub", "", "subject: the source system, e.g. ie.example.gateway")
	scopes := fl.String("scopes", strings.Join([]string{auth.ScopeAnimalsRead, auth.ScopeObservationsWrite, auth.ScopeRemarksRead, auth.ScopeRemarksWrite}, ","), "comma-separated scopes")
	locations := fl.String("location", "dk.herdnumber/123456", "comma-separated scheme/id locations")
	ttl := fl.Duration("ttl", 24*time.Hour, "token lifetime")
	issuer := fl.String("iss", "agritelem-dev", "issuer")
	audience := fl.String("aud", "agritelem-api", "audience")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if *sub == "" {
		return errors.New("-sub is required")
	}
	var locs []domain.Location
	for _, l := range strings.Split(*locations, ",") {
		scheme, id, ok := strings.Cut(strings.TrimSpace(l), "/")
		if !ok || scheme == "" || id == "" {
			return fmt.Errorf("location %q is not scheme/id", l)
		}
		locs = append(locs, domain.Location{Scheme: scheme, ID: id})
	}
	pem, err := os.ReadFile(*keyPath)
	if err != nil {
		return fmt.Errorf("read private key (run `devtoken keys` first): %w", err)
	}
	iss, err := auth.NewIssuer(pem, *issuer, *audience)
	if err != nil {
		return err
	}
	token, err := iss.Issue(*sub, strings.Split(*scopes, ","), locs, time.Now(), *ttl)
	if err != nil {
		return err
	}
	fmt.Println(token)
	return nil
}
