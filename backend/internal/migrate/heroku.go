package migrate

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Heroku's Platform API (an API key: heroku authorizations:create, or
// Account settings → API key). Applications keep building from Heroku Git
// with that key until they are pointed at their own repository.

var herokuAPI = "https://api.heroku.com"

func readHeroku(ctx context.Context, base, key string) (Plan, error) {
	var p Plan
	key = strings.TrimSpace(key)
	if key == "" {
		return p, errors.New("a Heroku API key is required")
	}
	api := httpAPI{base: strings.TrimRight(base, "/"), client: &http.Client{Timeout: 30 * time.Second},
		headers: map[string]string{"Authorization": "Bearer " + key, "Accept": "application/vnd.heroku+json; version=3"}}
	var apps []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		GitURL string `json:"git_url"`
		Team   *struct {
			Name string `json:"name"`
		} `json:"team"`
		Stack struct {
			Name string `json:"name"`
		} `json:"build_stack"`
	}
	if err := api.get(ctx, "/apps", &apps); err != nil {
		return p, err
	}
	for _, h := range apps {
		a := App{Ref: h.ID, Name: h.Name, Project: "heroku", Environment: "production", SourceType: "git", BuildPack: "nixpacks",
			GitRepo: h.GitURL, GitBranch: "main", GitToken: key, Port: 5000, Replicas: 1}
		if h.Team != nil && h.Team.Name != "" {
			a.Project = h.Team.Name
		}
		if a.GitRepo == "" {
			a.GitRepo = "https://git.heroku.com/" + h.Name + ".git"
		}
		a.Notes = append(a.Notes, "builds from Heroku Git with your API key until you point it at your own repository (Configuration tab)")

		// Processes: web is the application; the others need their own.
		var formation []struct {
			Type     string `json:"type"`
			Quantity int    `json:"quantity"`
			Command  string `json:"command"`
		}
		if err := api.get(ctx, "/apps/"+h.ID+"/formation", &formation); err == nil {
			for _, f := range formation {
				if f.Type == "web" {
					if f.Quantity > 0 {
						a.Replicas = f.Quantity
					}
					continue
				}
				if f.Quantity > 0 {
					a.Notes = append(a.Notes, fmt.Sprintf("the %s process (%s) is not imported: create another application from the same repository with it as start command", f.Type, f.Command))
				}
			}
		}
		var vars map[string]*string
		if err := api.get(ctx, "/apps/"+h.ID+"/config-vars", &vars); err != nil {
			a.Notes = append(a.Notes, "variables: "+err.Error())
		}
		keys := make([]string, 0, len(vars))
		for k := range vars {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		addonVars := map[string]string{}
		var addons []struct {
			Name    string `json:"name"`
			Service struct {
				Name string `json:"name"`
			} `json:"addon_service"`
			Plan struct {
				Name string `json:"name"`
			} `json:"plan"`
			ConfigVars []string `json:"config_vars"`
		}
		if err := api.get(ctx, "/apps/"+h.ID+"/addons", &addons); err == nil {
			for _, ad := range addons {
				engine, note := engineOf(ad.Service.Name)
				if engine == "" {
					a.Notes = append(a.Notes, fmt.Sprintf("the add-on %s (%s) stays on Heroku", ad.Name, ad.Plan.Name))
					continue
				}
				db := Database{Ref: h.ID + "/" + ad.Name, Name: h.Name + "-" + engine, Project: a.Project, Environment: a.Environment, Engine: engine,
					Notes: []string{"created empty: restore a dump of " + ad.Name + " (heroku pg:backups:download) into it"}}
				if note != "" {
					db.Notes = append(db.Notes, note)
				}
				p.Databases = append(p.Databases, db)
				for _, v := range ad.ConfigVars {
					addonVars[v] = ad.Name
				}
			}
		}
		for _, k := range keys {
			if vars[k] == nil {
				continue
			}
			if ad, ok := addonVars[k]; ok {
				a.Notes = append(a.Notes, fmt.Sprintf("%s still points at the add-on %s on Heroku: change it to the new database's address once its data is moved", k, ad))
			}
			a.Env = append(a.Env, Var{Key: k, Value: *vars[k], Secret: isSecretKey(k) || addonVars[k] != ""})
		}
		var domains []struct {
			Hostname string `json:"hostname"`
			Kind     string `json:"kind"`
		}
		if err := api.get(ctx, "/apps/"+h.ID+"/domains", &domains); err == nil {
			for _, d := range domains {
				if d.Kind == "custom" {
					a.Domains = append(a.Domains, d.Hostname)
				}
			}
		}
		if vars["PORT"] == nil {
			a.Notes = append(a.Notes, "Heroku gives the application its port in $PORT: PORT is set to the application's port (5000); change both if it listens elsewhere")
			a.Env = append(a.Env, Var{Key: "PORT", Value: "5000"})
		}
		p.Apps = append(p.Apps, a)
	}
	return p, nil
}
