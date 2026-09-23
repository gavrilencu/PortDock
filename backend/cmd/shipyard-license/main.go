// Command shipyard-license is the vendor tool that creates signing keys and
// issues Shipyard license keys. It is not shipped to customers.
//
//	shipyard-license keygen  -out ~/.shipyard-license
//	shipyard-license issue   -key ~/.shipyard-license/private.key -customer "Acme" -plan enterprise -days 365
//	shipyard-license inspect SY1....
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shipyard/shipyard/internal/license"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Args[2:])
	case "issue":
		err = issue(os.Args[2:])
	case "inspect":
		err = inspect(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: shipyard-license keygen|issue|inspect [flags]")
	os.Exit(2)
}

func keygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	out := fs.String("out", ".", "directory for private.key and public.key")
	_ = fs.Parse(args)
	priv := filepath.Join(*out, "private.key")
	if _, err := os.Stat(priv); err == nil {
		return fmt.Errorf("%s already exists — refusing to overwrite a signing key", priv)
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(priv, []byte(base64.StdEncoding.EncodeToString(key)+"\n"), 0o600); err != nil {
		return err
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	if err := os.WriteFile(filepath.Join(*out, "public.key"), []byte(pubB64+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Printf("Private key: %s (keep it secret and backed up)\nPublic key:  %s\n", priv, pubB64)
	return nil
}

func issue(args []string) error {
	fs := flag.NewFlagSet("issue", flag.ExitOnError)
	keyPath := fs.String("key", "", "path to private.key")
	customer := fs.String("customer", "", "customer name")
	email := fs.String("email", "", "customer contact email")
	plan := fs.String("plan", "enterprise", "community | pro | enterprise")
	days := fs.Int("days", 365, "validity in days")
	servers := fs.Int("servers", 0, "server limit override (-1 = unlimited, 0 = plan default)")
	users := fs.Int("users", 0, "user limit override (-1 = unlimited, 0 = plan default)")
	features := fs.String("features", "", "extra comma-separated features")
	_ = fs.Parse(args)
	if *keyPath == "" || *customer == "" {
		return fmt.Errorf("-key and -customer are required")
	}
	if _, ok := license.Plans[*plan]; !ok {
		return fmt.Errorf("unknown plan %q", *plan)
	}
	raw, err := os.ReadFile(*keyPath)
	if err != nil {
		return err
	}
	priv, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(priv) != ed25519.PrivateKeySize {
		return fmt.Errorf("%s is not an Ed25519 private key", *keyPath)
	}
	idBytes := make([]byte, 8)
	_, _ = rand.Read(idBytes)
	now := time.Now().UTC()
	l := license.License{
		ID: fmt.Sprintf("lic_%x", idBytes), Customer: *customer, Email: *email, Plan: *plan,
		MaxServers: *servers, MaxUsers: *users, IssuedAt: now.Format(time.RFC3339),
		ExpiresAt: now.Add(time.Duration(*days) * 24 * time.Hour).Format(time.RFC3339),
	}
	for _, f := range strings.Split(*features, ",") {
		if f = strings.TrimSpace(f); f != "" {
			l.Features = append(l.Features, f)
		}
	}
	key, err := license.Sign(l, ed25519.PrivateKey(priv))
	if err != nil {
		return err
	}
	fmt.Println(key)
	return nil
}

func inspect(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: shipyard-license inspect <key>")
	}
	l, err := license.Parse(args[0])
	if err != nil {
		return err
	}
	out, _ := json.MarshalIndent(l, "", "  ")
	fmt.Println(string(out))
	return nil
}
