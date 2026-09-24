package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/composefile"
	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/proxy"
	"github.com/portdock/portdock/internal/store"
)

// Versions: every successful production build becomes a numbered version (v1,
// v2…) whose images stay on the servers, so any of them can be deployed again
// in seconds — with the application's current variables, without a build. For
// compose applications each built service (frontend, backend…) is tagged.

const (
	// VersionsKept is how many previous versions are kept besides the live one.
	VersionsKept = 5
	// VersionMaxAge is how long a previous version is kept.
	VersionMaxAge = 15 * 24 * time.Hour
	// TriggerPromote deploys an existing version.
	TriggerPromote = "promote"
)

func versionTag(n int) string { return fmt.Sprintf("v%d", n) }

var versionTagRe = regexp.MustCompile(`:v\d+$`)

// versionRepo is the local repository holding the version tags of an
// application ("" service) or of one of its compose services.
func versionRepo(app store.Application, service string) string {
	if service == "" {
		return "portdock/" + strings.ToLower(app.UUID)
	}
	return "portdock/" + strings.ToLower(app.UUID) + "-" + proxy.SafeName(service)
}

// VersionServices decodes the per-service images of a compose version.
func VersionServices(v store.AppVersion) map[string]string {
	out := map[string]string{}
	_ = json.Unmarshal([]byte(v.Services), &out)
	return out
}

// LiveVersionID is the version served by the last successful production deployment.
func (e *Engine) LiveVersionID(ctx context.Context, appID int64) int64 {
	var id int64
	_ = e.db.QueryRowContext(ctx, `SELECT version_id FROM deployments WHERE application_id = ? AND pr_number = 0
		AND status = 'success' AND version_id > 0 ORDER BY id DESC LIMIT 1`, appID).Scan(&id)
	return id
}

func (e *Engine) nextVersionNumber(ctx context.Context, appID int64) int {
	var n int
	_ = e.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(number), 0) + 1 FROM app_versions WHERE application_id = ?", appID).Scan(&n)
	return n
}

// insertVersion stores v and links it to the deployment that built it.
func (e *Engine) insertVersion(ctx context.Context, v *store.AppVersion) error {
	v.Status = "available"
	if v.CreatedAt == "" {
		v.CreatedAt = store.Now()
	}
	if _, err := store.Insert(ctx, e.db, "app_versions", v); err != nil {
		return err
	}
	if v.DeploymentID != 0 {
		_, _ = e.db.ExecContext(ctx, "UPDATE deployments SET version_id = ? WHERE id = ?", v.ID, v.DeploymentID)
	}
	return nil
}

// recordImageVersion creates the version of a single-container build: image
// gets an immutable v<N> tag on every server.
func (j *job) recordImageVersion(ctx context.Context, targets []target, image string) {
	e, app := j.e, j.app
	if e.nextVersionNumber(ctx, app.ID) == 1 {
		e.backfillVersions(ctx, app, targets, j.d.ID)
	}
	n := e.nextVersionNumber(ctx, app.ID)
	tag := versionRepo(app, "") + ":" + versionTag(n)
	for _, t := range targets {
		if err := t.x.Run(ctx, "docker tag "+q(image)+" "+q(tag), executor.RunOpts{}); err != nil {
			j.log.Printf("⚠ %s: could not tag the version (%v); it keeps using %s", t.srv.Name, err, image)
			tag = image
			break
		}
	}
	v := store.AppVersion{ApplicationID: app.ID, Number: n, DeploymentID: j.d.ID, CommitSHA: j.d.CommitSHA, CommitMessage: j.d.CommitMessage, Branch: j.d.Branch, Image: tag}
	if err := e.insertVersion(ctx, &v); err != nil {
		j.log.Printf("⚠ could not record the version: %v", err)
		return
	}
	j.d.VersionID = v.ID
	j.log.Printf("📦 Version %s saved (%s).", versionTag(n), tag)
	e.pruneVersions(ctx, app, v.ID)
}

// backfillVersions turns the builds deployed before versions existed into
// versions, as long as their images are still on the primary server.
func (e *Engine) backfillVersions(ctx context.Context, app store.Application, targets []target, currentID int64) {
	prev, err := store.Select[store.Deployment](ctx, e.db, `SELECT `+store.DeploymentCols+` FROM deployments WHERE application_id = ? AND pr_number = 0
		AND status = 'success' AND rollback_of = 0 AND image != '' AND id < ? ORDER BY id DESC LIMIT ?`, app.ID, currentID, VersionsKept)
	if err != nil || len(targets) == 0 {
		return
	}
	for i := len(prev) - 1; i >= 0; i-- { // oldest first, so numbers follow the history
		d := prev[i]
		if !hasImage(ctx, targets[0].x, d.Image) {
			continue
		}
		n := e.nextVersionNumber(ctx, app.ID)
		tag := versionRepo(app, "") + ":" + versionTag(n)
		for _, t := range targets {
			if t.x.Run(ctx, "docker tag "+q(d.Image)+" "+q(tag)+" >/dev/null 2>&1", executor.RunOpts{}) != nil {
				tag = d.Image
			}
		}
		v := store.AppVersion{ApplicationID: app.ID, Number: n, DeploymentID: d.ID, CommitSHA: d.CommitSHA, CommitMessage: d.CommitMessage,
			Branch: d.Branch, Image: tag, CreatedAt: firstNonEmpty(d.FinishedAt, d.CreatedAt)}
		_ = e.insertVersion(ctx, &v)
	}
}

// recordComposeVersion tags the image of every built service on every server
// and keeps the compose file, so the whole stack can be deployed again as it was.
func (j *job) recordComposeVersion(ctx context.Context, targets []target) {
	e, app := j.e, j.app
	file := j.e.composeFile(app)
	raw, err := executor.Output(ctx, targets[0].x, "cat "+q(file))
	if err != nil {
		j.log.Printf("⚠ could not record the version: %v", err)
		return
	}
	services, _ := composefile.Services(raw)
	n := e.nextVersionNumber(ctx, app.ID)
	images := map[string]string{}
	for _, s := range services {
		if !s.Build {
			continue // image services come back through the saved compose file
		}
		tag := versionRepo(app, s.Name) + ":" + versionTag(n)
		ok := true
		for _, t := range targets {
			cmd := fmt.Sprintf(`cid=$(docker ps -q --filter label=com.docker.compose.project=%s --filter label=com.docker.compose.service=%s | head -n 1); `+
				`[ -n "$cid" ] && docker tag "$(docker inspect -f '{{.Image}}' "$cid")" %s`, q(composeProject(app.UUID)), q(s.Name), q(tag))
			if err := t.x.Run(ctx, cmd, executor.RunOpts{}); err != nil {
				j.log.Printf("⚠ %s: could not tag service %s: %v", t.srv.Name, s.Name, err)
				ok = false
				break
			}
		}
		if ok {
			images[s.Name] = tag
		}
	}
	b, _ := json.Marshal(images)
	v := store.AppVersion{ApplicationID: app.ID, Number: n, DeploymentID: j.d.ID, CommitSHA: j.d.CommitSHA, CommitMessage: j.d.CommitMessage,
		Branch: j.d.Branch, Image: "compose@" + versionTag(n), Services: string(b), ComposeFile: raw}
	if err := e.insertVersion(ctx, &v); err != nil {
		j.log.Printf("⚠ could not record the version: %v", err)
		return
	}
	j.d.VersionID = v.ID
	j.log.Printf("📦 Version %s saved (%d service image(s)).", versionTag(n), len(images))
	e.pruneVersions(ctx, app, v.ID)
}

// pruneVersions keeps the live version and the VersionsKept most recent
// previous versions younger than VersionMaxAge; the rest expire and their image
// tags are removed from the servers.
func (e *Engine) pruneVersions(ctx context.Context, app store.Application, live int64) {
	if live == 0 {
		live = e.LiveVersionID(ctx, app.ID)
	}
	vs, err := store.Select[store.AppVersion](ctx, e.db, "SELECT * FROM app_versions WHERE application_id = ? AND status = 'available' ORDER BY number DESC", app.ID)
	if err != nil {
		return
	}
	expired := versionsToExpire(vs, live, time.Now())
	if len(expired) == 0 {
		return
	}
	var tags []string
	for _, v := range expired {
		if versionTagRe.MatchString(v.Image) && strings.HasPrefix(v.Image, "portdock/") {
			tags = append(tags, v.Image)
		}
		for _, img := range VersionServices(v) {
			tags = append(tags, img)
		}
		_, _ = e.db.ExecContext(ctx, "UPDATE app_versions SET status = 'expired' WHERE id = ?", v.ID)
	}
	if len(tags) == 0 {
		return
	}
	quoted := make([]string, len(tags))
	for i, t := range tags {
		quoted[i] = q(t)
	}
	_ = e.eachTarget(ctx, app, func(t target) error {
		// Removing a tag frees the image only when nothing else uses it.
		return t.x.Run(ctx, "docker rmi "+strings.Join(quoted, " ")+" >/dev/null 2>&1; true", executor.RunOpts{})
	})
}

// versionsToExpire picks, from versions sorted newest first, those beyond the
// live one and the VersionsKept most recent previous ones younger than VersionMaxAge.
func versionsToExpire(vs []store.AppVersion, live int64, now time.Time) []store.AppVersion {
	kept := 0
	var out []store.AppVersion
	for _, v := range vs {
		if v.ID == live {
			continue
		}
		created, _ := time.Parse(time.RFC3339, v.CreatedAt)
		if kept < VersionsKept && now.Sub(created) < VersionMaxAge {
			kept++
			continue
		}
		out = append(out, v)
	}
	return out
}

// VersionRetention expires old versions of every application once an hour, so
// previous versions don't outlive VersionMaxAge when nothing is deployed.
func (e *Engine) VersionRetention(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		apps, _ := store.Select[store.Application](ctx, e.db, "SELECT * FROM applications WHERE id IN (SELECT application_id FROM app_versions WHERE status = 'available')")
		for _, app := range apps {
			e.pruneVersions(ctx, app, 0)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
