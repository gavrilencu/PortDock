package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Dokku, read with its command line on a server PortDock manages (as root,
// or as the dokku user whose SSH commands are Dokku's). Each application is
// imported from the image Dokku built (dokku/<app>:latest) on that server,
// with its variables, domains, port, scale and storage.

func readDokku(ctx context.Context, run Runner) (Plan, error) {
	var p Plan
	if run == nil {
		return p, errors.New("choose the server where Dokku runs")
	}
	// As root the command is "dokku …"; as the dokku user, SSH runs Dokku itself.
	prefix := "dokku "
	out, err := run(ctx, "dokku --quiet apps:list")
	if err != nil {
		prefix = ""
		if out, err = run(ctx, "--quiet apps:list"); err != nil {
			return p, fmt.Errorf("Dokku does not answer on this server: %w", err)
		}
	}
	dokku := func(args string) (string, error) { return run(ctx, prefix+args) }
	for _, name := range strings.Fields(out) {
		if strings.HasPrefix(name, "=====>") || name == "=====>" || !dokkuName.MatchString(name) {
			continue
		}
		a := App{Ref: name, Name: name, Project: "dokku", Environment: "production", SourceType: "image", BuildPack: "image",
			Image: "dokku/" + name + ":latest", Port: 5000, Replicas: 1}
		a.Notes = append(a.Notes, "runs the image Dokku built on this server; point it at its repository (Configuration tab) to build it in PortDock")
		if cfg, err := dokku("config:export --format json " + name); err == nil {
			var vars map[string]string
			if json.Unmarshal([]byte(strings.TrimSpace(cfg)), &vars) == nil {
				for k, v := range vars {
					if strings.HasPrefix(k, "DOKKU_") || k == "GIT_REV" {
						continue
					}
					a.Env = append(a.Env, Var{Key: k, Value: v, Secret: isSecretKey(k)})
					if strings.HasPrefix(k, "DATABASE_URL") || strings.HasPrefix(k, "REDIS_URL") {
						a.Notes = append(a.Notes, k+" points at a Dokku service: change it once the data is moved")
					}
				}
			}
		} else {
			a.Notes = append(a.Notes, "variables: "+err.Error())
		}
		if d, err := dokku("domains:report " + name + " --domains-app-vhosts"); err == nil {
			// Without the address Dokku gives every app (<app>.<global domain>).
			global, _ := dokku("domains:report " + name + " --domains-global-vhosts")
			for _, host := range strings.Fields(d) {
				if !slices.Contains(strings.Fields(global), strings.TrimPrefix(host, name+".")) {
					a.Domains = append(a.Domains, host)
				}
			}
		}
		// The ports set by hand, else the ones Dokku detected.
		for _, flag := range []string{"--ports-map", "--ports-map-detected"} {
			if ports, err := dokku("ports:report " + name + " " + flag); err == nil {
				if port := dokkuPort(ports); port > 0 {
					a.Port = port
					break
				}
			}
		}
		if scale, err := dokku("ps:scale " + name); err == nil {
			for proc, n := range dokkuScale(scale) {
				if proc == "web" {
					if n > 0 {
						a.Replicas = n
					}
				} else if n > 0 {
					a.Notes = append(a.Notes, fmt.Sprintf("the %s process (×%d) is not imported: create another application from the same image with its command", proc, n))
				}
			}
		}
		if mounts, err := dokku("storage:report " + name + " --storage-deploy-mounts"); err == nil {
			for _, m := range dokkuMounts(mounts) {
				a.Volumes = append(a.Volumes, Volume{Name: resourceName(name + "-" + lastSegment(m[1])), Path: m[1]})
				what := "the server directory " + m[0]
				if !strings.Contains(m[0], "/") {
					what = "the Docker volume " + m[0]
				}
				a.Notes = append(a.Notes, fmt.Sprintf("%s was %s: it became an empty volume, copy its files into it", m[1], what))
			}
		}
		p.Apps = append(p.Apps, a)
	}
	// Datastore plugins: empty databases of the same engines.
	for _, plugin := range []string{"postgres", "mysql", "mariadb", "mongo", "redis"} {
		list, err := dokku(plugin + ":list")
		if err != nil {
			continue
		}
		engine, _ := engineOf(plugin)
		for _, line := range strings.Split(list, "\n") {
			fields := strings.Fields(line)
			if len(fields) == 0 || strings.HasPrefix(line, "=====>") || strings.EqualFold(fields[0], "NAME") || !dokkuName.MatchString(fields[0]) {
				continue
			}
			p.Databases = append(p.Databases, Database{Ref: plugin + "/" + fields[0], Name: fields[0], Project: "dokku", Environment: "production", Engine: engine,
				Notes: []string{"created empty: restore a dump of it (dokku " + plugin + ":export " + fields[0] + ") into it"}})
		}
	}
	return p, nil
}

var (
	dokkuName   = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*$`)
	dokkuMap    = regexp.MustCompile(`(?:https?):(\d+):(\d+)`)
	dokkuVolume = regexp.MustCompile(`-v\s+(\S+?):(\S+)`)
)

// dokkuScale reads the process counts of "dokku ps:scale APP" (web: 1).
func dokkuScale(out string) map[string]int {
	res := map[string]int{}
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || strings.HasPrefix(k, "--") || strings.Contains(k, " ") {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			res[k] = n
		}
	}
	return res
}

// dokkuPort is the container port of the first http(s) port mapping.
func dokkuPort(out string) int {
	if m := dokkuMap.FindStringSubmatch(out); m != nil {
		n, _ := strconv.Atoi(m[2])
		return n
	}
	return 0
}

// dokkuMounts lists the host:container pairs of "-v host:container[:opts]".
func dokkuMounts(out string) [][2]string {
	var res [][2]string
	for _, m := range dokkuVolume.FindAllStringSubmatch(out, -1) {
		target, _, _ := strings.Cut(m[2], ":")
		res = append(res, [2]string{m[1], target})
	}
	return res
}

func lastSegment(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
