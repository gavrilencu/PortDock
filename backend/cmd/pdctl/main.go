// Command pdctl drives PortDock from a terminal or a CI pipeline, with an API
// token (Settings → API tokens, or a service account's token).
//
//	pdctl login --url https://portdock.example.com --token pd_…
//	pdctl apply -f portdock.yaml [--dry-run]
//	pdctl deploy shop/api --wait
//
// PORTDOCK_URL and PORTDOCK_TOKEN override the saved login (handy in CI).
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

var version = "dev"

const usage = `pdctl — PortDock from the command line

Usage:
  pdctl login --url URL [--token TOKEN]     save the address and an API token
  pdctl whoami                              show the account of the token
  pdctl apps                                list the applications
  pdctl deploy APP [--wait] [--commit SHA] [--force] [--timeout 30m]
  pdctl env list APP
  pdctl env set APP KEY=VALUE [--secret]
  pdctl env set APP KEY --from STORE:PATH#KEY   read it from Vault / Key Vault
  pdctl env unset APP KEY
  pdctl jobs APP                            list the scheduled jobs
  pdctl run APP JOB [--wait]                run a job now
  pdctl apply -f portdock.yaml [--dry-run]  apply configuration as code
  pdctl export APP [-o portdock.yaml]       render an application as portdock.yaml
  pdctl mcp                                 serve MCP on stdin/stdout for AI clients
  pdctl template lint|bundle|convert …      service templates (pdctl template help)
  pdctl version

APP is a name, project/name or project/environment/name.
PORTDOCK_URL and PORTDOCK_TOKEN override the saved login.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "pdctl:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Print(usage)
		return nil
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "version":
		fmt.Println("pdctl", version)
		return nil
	case "login":
		return login(rest)
	case "template":
		return template(rest)
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	switch cmd {
	case "whoami":
		var me struct{ Email, Name, Role string }
		if err := c.do("GET", "/api/auth/me", nil, &me); err != nil {
			return err
		}
		fmt.Printf("%s (%s, %s) on %s\n", me.Email, me.Name, me.Role, c.url)
		return nil
	case "apps":
		return c.apps()
	case "deploy":
		return c.deploy(rest)
	case "env":
		return c.env(rest)
	case "jobs":
		return c.jobs(rest)
	case "run":
		return c.runJob(rest)
	case "apply":
		return c.apply(rest)
	case "export":
		return c.export(rest)
	case "mcp":
		return c.mcpBridge(os.Stdin, os.Stdout)
	}
	return fmt.Errorf("unknown command %q (pdctl help)", cmd)
}

// ---- configuration ----

type config struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

func configPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "portdock", "pdctl.json")
}

func login(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	url := fs.String("url", "", "PortDock address, e.g. https://portdock.example.com")
	token := fs.String("token", "", "API token (read from standard input when omitted)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *url == "" {
		return errors.New("--url is required")
	}
	if *token == "" {
		fmt.Fprint(os.Stderr, "API token: ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		*token = strings.TrimSpace(line)
	}
	c := &client{url: strings.TrimRight(*url, "/"), token: *token, http: &http.Client{Timeout: 60 * time.Second}}
	var me struct{ Email string }
	if err := c.do("GET", "/api/auth/me", nil, &me); err != nil {
		return fmt.Errorf("the token does not work: %w", err)
	}
	b, _ := json.MarshalIndent(config{URL: c.url, Token: c.token}, "", "  ")
	p := configPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return err
	}
	fmt.Printf("Signed in to %s as %s (saved in %s)\n", c.url, me.Email, p)
	return nil
}

// ---- HTTP ----

type client struct {
	url, token string
	http       *http.Client
}

func newClient() (*client, error) {
	var cfg config
	if b, err := os.ReadFile(configPath()); err == nil {
		_ = json.Unmarshal(b, &cfg)
	}
	if v := os.Getenv("PORTDOCK_URL"); v != "" {
		cfg.URL = v
	}
	if v := os.Getenv("PORTDOCK_TOKEN"); v != "" {
		cfg.Token = v
	}
	if cfg.URL == "" || cfg.Token == "" {
		return nil, errors.New("not signed in: run pdctl login, or set PORTDOCK_URL and PORTDOCK_TOKEN")
	}
	return &client{url: strings.TrimRight(cfg.URL, "/"), token: cfg.Token, http: &http.Client{Timeout: 5 * time.Minute}}, nil
}

// raw sends a request and returns the response body.
func (c *client) raw(method, path, contentType string, body io.Reader) ([]byte, int, error) {
	req, err := http.NewRequest(method, c.url+path, body)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", "pdctl/"+version)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return b, resp.StatusCode, err
}

func (c *client) do(method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	b, code, err := c.raw(method, path, "application/json", body)
	if err != nil {
		return err
	}
	if code >= 300 {
		return apiError(code, b)
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

func apiError(code int, b []byte) error {
	var e struct{ Error string }
	if json.Unmarshal(b, &e) == nil && e.Error != "" {
		return fmt.Errorf("%s (HTTP %d)", e.Error, code)
	}
	return fmt.Errorf("HTTP %d: %s", code, strings.TrimSpace(string(b)))
}

// ---- applications ----

type app struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	EnvironmentID int64  `json:"environment_id"`
	Status        string `json:"status"`
	CurrentImage  string `json:"current_image"`
}

type place struct{ project, env string }

func (c *client) places() (map[int64]place, error) {
	var projects []struct {
		Name         string `json:"name"`
		Environments []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"environments"`
	}
	if err := c.do("GET", "/api/projects", nil, &projects); err != nil {
		return nil, err
	}
	out := map[int64]place{}
	for _, p := range projects {
		for _, e := range p.Environments {
			out[e.ID] = place{p.Name, e.Name}
		}
	}
	return out, nil
}

// findApp resolves name, project/name or project/environment/name.
func (c *client) findApp(ref string) (app, error) {
	parts := strings.Split(ref, "/")
	name := parts[len(parts)-1]
	var apps []app
	if err := c.do("GET", "/api/applications", nil, &apps); err != nil {
		return app{}, err
	}
	places, err := c.places()
	if err != nil {
		return app{}, err
	}
	var found []app
	for _, a := range apps {
		pl := places[a.EnvironmentID]
		if a.Name != name ||
			(len(parts) >= 2 && pl.project != parts[0]) ||
			(len(parts) == 3 && pl.env != parts[1]) {
			continue
		}
		found = append(found, a)
	}
	switch len(found) {
	case 0:
		return app{}, fmt.Errorf("application %q not found", ref)
	case 1:
		return found[0], nil
	}
	var names []string
	for _, a := range found {
		pl := places[a.EnvironmentID]
		names = append(names, pl.project+"/"+pl.env+"/"+a.Name)
	}
	return app{}, fmt.Errorf("%q is ambiguous: %s", ref, strings.Join(names, ", "))
}

func (c *client) apps() error {
	var apps []app
	if err := c.do("GET", "/api/applications", nil, &apps); err != nil {
		return err
	}
	places, err := c.places()
	if err != nil {
		return err
	}
	sort.Slice(apps, func(i, j int) bool {
		pi, pj := places[apps[i].EnvironmentID], places[apps[j].EnvironmentID]
		return pi.project+pi.env+apps[i].Name < pj.project+pj.env+apps[j].Name
	})
	w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tAPPLICATION\tPROJECT\tENVIRONMENT\tSTATUS")
	for _, a := range apps {
		pl := places[a.EnvironmentID]
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n", a.ID, a.Name, pl.project, pl.env, a.Status)
	}
	return w.Flush()
}

func parseFlags(name string, args []string, positional int, define func(fs *flag.FlagSet)) ([]string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	define(fs)
	// Flags may follow the positional arguments.
	var pos, flags []string
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			flags = append(flags, args[i])
			if !strings.Contains(args[i], "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") && !isBoolFlag(fs, args[i]) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		pos = append(pos, args[i])
	}
	if err := fs.Parse(flags); err != nil {
		return nil, err
	}
	if len(pos) != positional {
		return nil, fmt.Errorf("%s: expected %d argument(s) (pdctl help)", name, positional)
	}
	return pos, nil
}

func isBoolFlag(fs *flag.FlagSet, arg string) bool {
	f := fs.Lookup(strings.TrimLeft(arg, "-"))
	if f == nil {
		return false
	}
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

type deployment struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
	Logs   string `json:"logs"`
	Image  string `json:"image"`
}

func (c *client) deploy(args []string) error {
	var wait, force bool
	var commit string
	var timeout time.Duration
	pos, err := parseFlags("deploy", args, 1, func(fs *flag.FlagSet) {
		fs.BoolVar(&wait, "wait", false, "wait for the deployment to finish")
		fs.BoolVar(&force, "force", false, "rebuild without the cache")
		fs.StringVar(&commit, "commit", "", "deploy this commit")
		fs.DurationVar(&timeout, "timeout", 30*time.Minute, "how long --wait waits")
	})
	if err != nil {
		return err
	}
	a, err := c.findApp(pos[0])
	if err != nil {
		return err
	}
	var d deployment
	if err := c.do("POST", fmt.Sprintf("/api/applications/%d/deploy", a.ID), map[string]any{"force_rebuild": force, "commit_sha": commit}, &d); err != nil {
		return err
	}
	fmt.Printf("Deployment #%d of %s: %s\n", d.ID, a.Name, d.Status)
	if !wait {
		return nil
	}
	deadline := time.Now().Add(timeout)
	last := d.Status
	for time.Now().Before(deadline) {
		time.Sleep(3 * time.Second)
		if err := c.do("GET", fmt.Sprintf("/api/deployments/%d", d.ID), nil, &d); err != nil {
			return err
		}
		if d.Status != last {
			fmt.Printf("  %s\n", d.Status)
			last = d.Status
		}
		switch d.Status {
		case "success":
			fmt.Printf("Deployed %s\n", d.Image)
			return nil
		case "failed", "cancelled", "rejected":
			lines := strings.Split(strings.TrimRight(d.Logs, "\n"), "\n")
			fmt.Println(strings.Join(lines[max(0, len(lines)-30):], "\n"))
			return fmt.Errorf("deployment #%d %s", d.ID, d.Status)
		}
	}
	return fmt.Errorf("deployment #%d still %s after %s", d.ID, d.Status, timeout)
}

// ---- variables ----

type envVar struct {
	ID       int64  `json:"id"`
	Key      string `json:"key"`
	Value    string `json:"value"`
	IsSecret bool   `json:"is_secret"`
	Service  string `json:"service"`
	IsBuild  bool   `json:"is_build"`
	Preview  bool   `json:"is_preview"`
	// A secret store reference: the value is read when a deployment starts.
	RefStoreID int64  `json:"ref_store_id"`
	RefPath    string `json:"ref_path"`
	RefKey     string `json:"ref_key"`
}

type secretStoreName struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func (c *client) secretStores() ([]secretStoreName, error) {
	var out []secretStoreName
	return out, c.do("GET", "/api/secret-stores/names", nil, &out)
}

// setEnvRef points a variable at a secret store: STORE:PATH#KEY.
func (c *client) setEnvRef(a app, cur *envVar, key, ref string) error {
	storeName, rest, found := strings.Cut(ref, ":")
	path, refKey, _ := strings.Cut(rest, "#")
	if !found || storeName == "" || path == "" {
		return errors.New("env set: use --from STORE:PATH#KEY, e.g. vault-prod:shop/db#password")
	}
	sts, err := c.secretStores()
	if err != nil {
		return err
	}
	var id int64
	for _, st := range sts {
		if st.Name == storeName {
			id = st.ID
		}
	}
	if id == 0 {
		return fmt.Errorf("no secret store named %s", storeName)
	}
	body := map[string]any{"ref_store_id": id, "ref_path": path, "ref_key": refKey}
	if cur != nil {
		err = c.do("PATCH", fmt.Sprintf("/api/envs/%d", cur.ID), body, nil)
	} else {
		body["key"] = key
		err = c.do("POST", fmt.Sprintf("/api/applications/%d/envs", a.ID), body, nil)
	}
	if err != nil {
		return err
	}
	fmt.Printf("%s of %s now reads %s (redeploy to apply)\n", key, a.Name, ref)
	return nil
}

func (c *client) env(args []string) error {
	if len(args) < 2 {
		return errors.New("env: list APP | set APP KEY=VALUE [--secret] | set APP KEY --from STORE:PATH#KEY | unset APP KEY")
	}
	sub := args[0]
	a, err := c.findApp(args[1])
	if err != nil {
		return err
	}
	var vars []envVar
	if err := c.do("GET", fmt.Sprintf("/api/applications/%d/envs", a.ID), nil, &vars); err != nil {
		return err
	}
	find := func(key string) *envVar {
		for i := range vars {
			if vars[i].Key == key && vars[i].Service == "" && !vars[i].Preview && !vars[i].IsBuild {
				return &vars[i]
			}
		}
		return nil
	}
	switch sub {
	case "list":
		names := map[int64]string{}
		for _, v := range vars {
			if v.RefStoreID > 0 && len(names) == 0 {
				sts, _ := c.secretStores()
				for _, st := range sts {
					names[st.ID] = st.Name
				}
			}
		}
		w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
		for _, v := range vars {
			val := v.Value
			switch {
			case v.RefStoreID > 0:
				val = "from " + names[v.RefStoreID] + ":" + v.RefPath
				if v.RefKey != "" {
					val += "#" + v.RefKey
				}
			case v.IsSecret:
				val = "••••••••"
			}
			fmt.Fprintf(w, "%s\t%s\n", v.Key, val)
		}
		return w.Flush()
	case "set":
		var secret bool
		var from string
		pos, err := parseFlags("env set", args[2:], 1, func(fs *flag.FlagSet) {
			fs.BoolVar(&secret, "secret", false, "store as a secret")
			fs.StringVar(&from, "from", "", "read the value from a secret store: STORE:PATH#KEY")
		})
		if err != nil {
			return err
		}
		if from != "" {
			return c.setEnvRef(a, find(pos[0]), pos[0], from)
		}
		key, value, ok := strings.Cut(pos[0], "=")
		if !ok {
			return errors.New("env set: use KEY=VALUE")
		}
		if v := find(key); v != nil {
			if err := c.do("PATCH", fmt.Sprintf("/api/envs/%d", v.ID), map[string]any{"value": value, "is_secret": secret || v.IsSecret}, nil); err != nil {
				return err
			}
		} else if err := c.do("POST", fmt.Sprintf("/api/applications/%d/envs", a.ID), map[string]any{"key": key, "value": value, "is_secret": secret}, nil); err != nil {
			return err
		}
		fmt.Printf("%s set on %s (redeploy to apply)\n", key, a.Name)
		return nil
	case "unset":
		if len(args) != 3 {
			return errors.New("env unset APP KEY")
		}
		v := find(args[2])
		if v == nil {
			return fmt.Errorf("%s is not set", args[2])
		}
		if err := c.do("DELETE", fmt.Sprintf("/api/envs/%d", v.ID), nil, nil); err != nil {
			return err
		}
		fmt.Printf("%s removed from %s (redeploy to apply)\n", args[2], a.Name)
		return nil
	}
	return fmt.Errorf("env: unknown command %q", sub)
}

// ---- jobs ----

type jobInfo struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Schedule   string `json:"schedule"`
	Enabled    bool   `json:"enabled"`
	LastStatus string `json:"last_status"`
	NextRunAt  string `json:"next_run_at"`
}

func (c *client) appJobs(ref string) (app, []jobInfo, error) {
	a, err := c.findApp(ref)
	if err != nil {
		return a, nil, err
	}
	var jobs []jobInfo
	err = c.do("GET", fmt.Sprintf("/api/applications/%d/jobs", a.ID), nil, &jobs)
	return a, jobs, err
}

func (c *client) jobs(args []string) error {
	if len(args) != 1 {
		return errors.New("jobs APP")
	}
	_, jobs, err := c.appJobs(args[0])
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "JOB\tSCHEDULE\tENABLED\tLAST\tNEXT")
	for _, j := range jobs {
		fmt.Fprintf(w, "%s\t%s\t%v\t%s\t%s\n", j.Name, j.Schedule, j.Enabled, j.LastStatus, j.NextRunAt)
	}
	return w.Flush()
}

func (c *client) runJob(args []string) error {
	var wait bool
	pos, err := parseFlags("run", args, 2, func(fs *flag.FlagSet) { fs.BoolVar(&wait, "wait", false, "wait for the run to finish") })
	if err != nil {
		return err
	}
	_, jobs, err := c.appJobs(pos[0])
	if err != nil {
		return err
	}
	var job *jobInfo
	for i := range jobs {
		if jobs[i].Name == pos[1] {
			job = &jobs[i]
		}
	}
	if job == nil {
		return fmt.Errorf("job %q not found", pos[1])
	}
	var r struct {
		ID       int64  `json:"id"`
		Status   string `json:"status"`
		ExitCode int    `json:"exit_code"`
		Output   string `json:"output"`
	}
	if err := c.do("POST", fmt.Sprintf("/api/jobs/%d/run", job.ID), nil, &r); err != nil {
		return err
	}
	fmt.Printf("Run #%d of %s started\n", r.ID, job.Name)
	if !wait {
		return nil
	}
	for r.Status == "running" {
		time.Sleep(2 * time.Second)
		if err := c.do("GET", fmt.Sprintf("/api/job-runs/%d", r.ID), nil, &r); err != nil {
			return err
		}
	}
	fmt.Print(r.Output)
	if r.Status != "success" {
		return fmt.Errorf("run #%d %s (exit code %d)", r.ID, r.Status, r.ExitCode)
	}
	return nil
}

// ---- configuration as code ----

func (c *client) apply(args []string) error {
	var file string
	var dry bool
	if _, err := parseFlags("apply", args, 0, func(fs *flag.FlagSet) {
		fs.StringVar(&file, "f", "portdock.yaml", "the file to apply")
		fs.BoolVar(&dry, "dry-run", false, "show the changes without making them")
	}); err != nil {
		return err
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	path := "/api/gitops/apply"
	if dry {
		path += "?dry_run=true"
	}
	out, code, err := c.raw("POST", path, "application/yaml", bytes.NewReader(b))
	if err != nil {
		return err
	}
	var res struct {
		DryRun  bool `json:"dry_run"`
		Changes []struct {
			Resource, Name, Action string
			Fields                 []string
		} `json:"changes"`
		Error string `json:"error"`
	}
	if json.Unmarshal(out, &res) != nil {
		return apiError(code, out)
	}
	changed := 0
	for _, ch := range res.Changes {
		sign := map[string]string{"create": "+", "update": "~", "delete": "-", "unchanged": " "}[ch.Action]
		line := fmt.Sprintf("%s %s %s", sign, ch.Resource, ch.Name)
		if len(ch.Fields) > 0 {
			line += " (" + strings.Join(ch.Fields, ", ") + ")"
		}
		if ch.Action != "unchanged" {
			changed++
			fmt.Println(line)
		}
	}
	if res.Error != "" {
		return errors.New(res.Error)
	}
	if code >= 300 {
		return apiError(code, out)
	}
	switch {
	case changed == 0:
		fmt.Println("Nothing to change.")
	case dry:
		fmt.Printf("%d change(s) to apply (dry run).\n", changed)
	default:
		fmt.Printf("%d change(s) applied.\n", changed)
	}
	return nil
}

func (c *client) export(args []string) error {
	var out string
	pos, err := parseFlags("export", args, 1, func(fs *flag.FlagSet) { fs.StringVar(&out, "o", "", "write to this file instead of standard output") })
	if err != nil {
		return err
	}
	a, err := c.findApp(pos[0])
	if err != nil {
		return err
	}
	b, code, err := c.raw("GET", fmt.Sprintf("/api/applications/%d/export", a.ID), "", nil)
	if err != nil {
		return err
	}
	if code >= 300 {
		return apiError(code, b)
	}
	if out == "" {
		_, err = os.Stdout.Write(b)
		return err
	}
	return os.WriteFile(out, b, 0o644)
}
