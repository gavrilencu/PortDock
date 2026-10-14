package secretstore

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestVault(t *testing.T) {
	logins := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/auth/approle/login" {
			logins++
			var in map[string]string
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in["role_id"] != "role" || in["secret_id"] != "sid" {
				w.WriteHeader(400)
				w.Write([]byte(`{"errors":["invalid role or secret ID"]}`))
				return
			}
			w.Write([]byte(`{"auth":{"client_token":"hvs.approle"}}`))
			return
		}
		tok := r.Header.Get("X-Vault-Token")
		if tok != "hvs.root" && tok != "hvs.approle" {
			w.WriteHeader(403)
			w.Write([]byte(`{"errors":["permission denied"]}`))
			return
		}
		if r.Header.Get("X-Vault-Namespace") != "team-a" {
			t.Errorf("namespace: %q", r.Header.Get("X-Vault-Namespace"))
		}
		switch r.URL.EscapedPath() {
		case "/v1/auth/token/lookup-self":
			w.Write([]byte(`{"data":{"policies":["portdock"]}}`))
		case "/v1/secret/data/shop/db":
			w.Write([]byte(`{"data":{"data":{"password":"s3cr=t","port":5432,"tls":true},"metadata":{"version":3}}}`))
		case "/v1/secret/data/shop/gone":
			w.Write([]byte(`{"data":{"data":null,"metadata":{"deletion_time":"2026-01-01T00:00:00Z"}}}`))
		case "/v1/secret/data/other/team":
			w.WriteHeader(403)
			w.Write([]byte(`{"errors":["1 error occurred:\n\t* permission denied\n\n"]}`))
		case "/v1/kv/legacy/app":
			w.Write([]byte(`{"data":{"API_KEY":"abc"}}`))
		case "/v1/secret/data/with%20space":
			w.Write([]byte(`{"data":{"data":{"k":"v"}}}`))
		default:
			w.WriteHeader(404)
			w.Write([]byte(`{"errors":[]}`))
		}
	}))
	defer srv.Close()

	c, err := New(Vault, srv.URL, Config{Token: "hvs.root", Namespace: "team-a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Check(t.Context()); err != nil {
		t.Fatal(err)
	}
	vals, err := c.Read(t.Context(), "shop/db")
	if err != nil || vals["password"] != "s3cr=t" || vals["port"] != "5432" || vals["tls"] != "true" {
		t.Fatalf("read: %v %v", vals, err)
	}
	if _, err := Lookup(vals, "user"); err == nil || !strings.Contains(err.Error(), "keys: password, port, tls") {
		t.Errorf("missing key: %v", err)
	}
	if v, _ := c.Read(t.Context(), "with space"); v["k"] != "v" {
		t.Errorf("escaped path: %v", v)
	}
	for path, want := range map[string]string{"shop/none": "no secret at secret/shop/none", "other/team": "permission denied on secret/other/team",
		"shop/gone": "latest version of secret/shop/gone is deleted", "../sys/raw": ". or ..", "/abs": "start or end"} {
		if _, err := c.Read(t.Context(), path); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", path, err)
		}
	}

	kv1, _ := New(Vault, srv.URL, Config{Token: "hvs.root", Namespace: "team-a", Mount: "kv", KVVersion: 1})
	if v, err := kv1.Read(t.Context(), "legacy/app"); err != nil || v["API_KEY"] != "abc" {
		t.Errorf("kv1: %v %v", v, err)
	}

	ar, _ := New(Vault, srv.URL, Config{Auth: "approle", RoleID: "role", SecretID: "sid", Namespace: "team-a"})
	for range 2 {
		if v, err := ar.Read(t.Context(), "shop/db"); err != nil || v["password"] != "s3cr=t" {
			t.Fatalf("approle: %v %v", v, err)
		}
	}
	if logins != 1 {
		t.Errorf("logins: %d (the token is reused)", logins)
	}
	bad, _ := New(Vault, srv.URL, Config{Auth: "approle", RoleID: "role", SecretID: "wrong", Namespace: "team-a"})
	if err := bad.Check(t.Context()); err == nil || !strings.Contains(err.Error(), "invalid role or secret ID") {
		t.Errorf("bad approle: %v", err)
	}
	wrong, _ := New(Vault, srv.URL, Config{Token: "hvs.nope", Namespace: "team-a"})
	if err := wrong.Check(t.Context()); err == nil || !strings.Contains(err.Error(), "not valid") {
		t.Errorf("bad token: %v", err)
	}
}

func TestKeyVault(t *testing.T) {
	const tenant, client = "11111111-2222-3333-4444-555555555555", "66666666-7777-8888-9999-000000000000"
	tokens := 0
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(b))
		if r.URL.Path != "/"+tenant+"/oauth2/v2.0/token" || form.Get("grant_type") != "client_credentials" || form.Get("scope") != "https://vault.azure.net/.default" {
			t.Errorf("token request: %s %v", r.URL.Path, form)
		}
		if form.Get("client_secret") != "pw" {
			w.WriteHeader(401)
			w.Write([]byte(`{"error":"invalid_client","error_description":"AADSTS7000215: Invalid client secret provided.\r\nTrace ID: x"}`))
			return
		}
		tokens++
		w.Write([]byte(`{"access_token":"eyJ.kv","expires_in":3599}`))
	}))
	defer idp.Close()
	kv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer eyJ.kv" || r.URL.Query().Get("api-version") != keyVaultAPI {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/secrets":
			w.WriteHeader(403)
			w.Write([]byte(`{"error":{"code":"Forbidden","message":"no list permission"}}`))
		case "/secrets/db-password":
			w.Write([]byte(`{"value":"pa55","attributes":{"enabled":true}}`))
		case "/secrets/db-password/0123456789abcdef0123456789abcdef":
			w.Write([]byte(`{"value":"old","attributes":{"enabled":true}}`))
		case "/secrets/shop-config":
			w.Write([]byte(`{"value":"{\"user\":\"shop\",\"port\":5432}","attributes":{"enabled":true}}`))
		case "/secrets/off":
			w.Write([]byte(`{"value":"x","attributes":{"enabled":false}}`))
		case "/secrets/private":
			w.WriteHeader(403)
			w.Write([]byte(`{"error":{"code":"Forbidden","message":"denied"}}`))
		default:
			w.WriteHeader(404)
			w.Write([]byte(`{"error":{"code":"SecretNotFound","message":"not found"}}`))
		}
	}))
	defer kv.Close()

	cfg := Config{TenantID: tenant, ClientID: client, ClientSecret: "pw", AuthorityHost: idp.URL}
	c, err := New(AzureKeyVault, kv.URL, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Check(t.Context()); err != nil {
		t.Fatalf("check without the list permission: %v", err)
	}
	if v, err := c.Read(t.Context(), "db-password"); err != nil || v[""] != "pa55" {
		t.Fatalf("read: %v %v", v, err)
	}
	if v, _ := c.Read(t.Context(), "db-password/0123456789abcdef0123456789abcdef"); v[""] != "old" {
		t.Errorf("version: %v", v)
	}
	v, _ := c.Read(t.Context(), "shop-config")
	if got, _ := Lookup(v, "user"); got != "shop" || v["port"] != "5432" {
		t.Errorf("json keys: %v", v)
	}
	if tokens != 1 {
		t.Errorf("tokens: %d (cached until it expires)", tokens)
	}
	for path, want := range map[string]string{"missing": "no secret named missing", "private": "Key Vault Secrets User", "off": "disabled", "bad_name": "letters, digits and dashes"} {
		if _, err := c.Read(t.Context(), path); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", path, err)
		}
	}
	cfg.ClientSecret = "wrong"
	bad, _ := New(AzureKeyVault, kv.URL, cfg)
	if err := bad.Check(t.Context()); err == nil || !strings.Contains(err.Error(), "Invalid client secret provided. (HTTP 401)") {
		t.Errorf("bad secret: %v", err)
	}

	// Managed identity: the metadata service, never through a proxy.
	imds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Metadata") != "true" || r.URL.Query().Get("resource") != "https://vault.azure.net" || r.URL.Query().Get("client_id") != client {
			t.Errorf("imds: %v %v", r.Header, r.URL.Query())
		}
		w.Write([]byte(`{"access_token":"eyJ.kv","expires_in":"86399"}`))
	}))
	defer imds.Close()
	mi, err := New(AzureKeyVault, kv.URL, Config{Auth: "managed_identity", ClientID: client})
	if err != nil {
		t.Fatal(err)
	}
	mi.(*keyVault).imdsURL = imds.URL
	if v, err := mi.Read(t.Context(), "db-password"); err != nil || v[""] != "pa55" {
		t.Errorf("managed identity: %v %v", v, err)
	}

	gov := &keyVault{base: "https://bank.vault.usgovcloudapi.net"}
	if gov.resource() != "https://vault.usgovcloudapi.net" {
		t.Errorf("sovereign cloud: %s", gov.resource())
	}
}

func TestValidate(t *testing.T) {
	for _, c := range []struct {
		typ, addr string
		cfg       Config
		want      string
	}{
		{Vault, "vault.example.com", Config{Token: "x"}, "http(s) URL"},
		{Vault, "https://vault:8200", Config{}, "token is required"},
		{Vault, "https://vault:8200", Config{Auth: "approle", RoleID: "r"}, "role ID and a secret ID"},
		{Vault, "https://vault:8200", Config{Token: "x", Mount: "../sys"}, "mount"},
		{Vault, "https://vault:8200", Config{Token: "x", CACert: "nope"}, "PEM"},
		{AzureKeyVault, "http://kv.vault.azure.net", Config{}, "https"},
		{AzureKeyVault, "https://kv.vault.azure.net", Config{TenantID: "t", ClientID: "c", ClientSecret: "s"}, "GUIDs"},
		{"aws", "https://x", Config{}, "unknown"},
	} {
		c.cfg.Defaults(c.typ)
		if err := Validate(c.typ, c.addr, c.cfg); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %s %+v: %v", c.typ, c.addr, c.cfg, err)
		}
	}
	ok := Config{Token: "x"}
	ok.Defaults(Vault)
	if err := Validate(Vault, "https://vault.example.com:8200", ok); err != nil {
		t.Error(err)
	}
	if _, err := Lookup(map[string]string{"": "whole", "a": "1"}, ""); err != nil {
		t.Error(err)
	}
	if _, err := Lookup(map[string]string{"a": "1", "b": "2"}, ""); err == nil || !strings.Contains(err.Error(), "name a key: a, b") {
		t.Errorf("lookup without key: %v", err)
	}
}
