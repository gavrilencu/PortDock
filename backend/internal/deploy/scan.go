package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/netcfg"
	"github.com/portdock/portdock/internal/notify"
	"github.com/portdock/portdock/internal/store"
)

// Vulnerability scanning runs Trivy (https://trivy.dev) as a container on the
// server that holds the image, reading it through the Docker socket. The
// vulnerability database is downloaded on first use and cached in a volume.

// DefaultTrivyImage is used unless the trivy_image setting names another.
const DefaultTrivyImage = "aquasec/trivy:0.58.2"

const (
	trivyCacheVolume = "portdock-trivy-cache"
	scansPerApp      = 30   // scans kept per application
	maxFindings      = 3000 // findings stored per scan
)

// Finding is a vulnerability of a package in an image.
type Finding struct {
	ID        string `json:"id"`
	Severity  string `json:"severity"` // CRITICAL | HIGH | MEDIUM | LOW | UNKNOWN
	Package   string `json:"package"`
	Installed string `json:"installed"`
	Fixed     string `json:"fixed"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	Target    string `json:"target"` // OS packages or the file (package-lock.json, app.jar…)
}

var severityRank = map[string]int{"CRITICAL": 0, "HIGH": 1, "MEDIUM": 2, "LOW": 3, "UNKNOWN": 4}

// ParseTrivy reads the JSON report of `trivy image --format json`.
func ParseTrivy(raw []byte) ([]Finding, error) {
	var rep struct {
		Results []struct {
			Target          string `json:"Target"`
			Vulnerabilities []struct {
				VulnerabilityID  string `json:"VulnerabilityID"`
				PkgName          string `json:"PkgName"`
				InstalledVersion string `json:"InstalledVersion"`
				FixedVersion     string `json:"FixedVersion"`
				Severity         string `json:"Severity"`
				Title            string `json:"Title"`
				PrimaryURL       string `json:"PrimaryURL"`
			} `json:"Vulnerabilities"`
		} `json:"Results"`
	}
	// Anything before the report (download progress on old versions) is skipped.
	if i := strings.IndexByte(string(raw), '{'); i > 0 {
		raw = raw[i:]
	}
	if err := json.Unmarshal(raw, &rep); err != nil {
		return nil, fmt.Errorf("unexpected Trivy output: %w", err)
	}
	seen := map[string]bool{}
	var out []Finding
	for _, r := range rep.Results {
		for _, v := range r.Vulnerabilities {
			key := v.VulnerabilityID + "|" + v.PkgName + "|" + v.InstalledVersion
			if seen[key] {
				continue
			}
			seen[key] = true
			sev := strings.ToUpper(v.Severity)
			if _, ok := severityRank[sev]; !ok {
				sev = "UNKNOWN"
			}
			out = append(out, Finding{ID: v.VulnerabilityID, Severity: sev, Package: v.PkgName, Installed: v.InstalledVersion,
				Fixed: v.FixedVersion, Title: v.Title, URL: v.PrimaryURL, Target: r.Target})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if severityRank[out[i].Severity] != severityRank[out[j].Severity] {
			return severityRank[out[i].Severity] < severityRank[out[j].Severity]
		}
		return out[i].ID > out[j].ID // newest CVEs first
	})
	return out, nil
}

// countFindings fills the severity counters of a scan.
func countFindings(s *store.ImageScan, fs []Finding) {
	s.Critical, s.High, s.Medium, s.Low, s.Unknown, s.Fixable = 0, 0, 0, 0, 0, 0
	for _, f := range fs {
		switch f.Severity {
		case "CRITICAL":
			s.Critical++
		case "HIGH":
			s.High++
		case "MEDIUM":
			s.Medium++
		case "LOW":
			s.Low++
		default:
			s.Unknown++
		}
		if f.Fixed != "" {
			s.Fixable++
		}
	}
}

// ScanBlocks reports whether a scan violates the blocking policy of an application.
func ScanBlocks(policy string, s store.ImageScan) bool {
	switch policy {
	case "critical":
		return s.Critical > 0
	case "high":
		return s.Critical+s.High > 0
	}
	return false
}

func (e *Engine) trivyImage(ctx context.Context) string {
	if v := strings.TrimSpace(e.db.Setting(ctx, "trivy_image")); v != "" {
		return v
	}
	return DefaultTrivyImage
}

func trivyCmd(scanner, image string, ignoreUnfixed bool, net netcfg.Config) string {
	args := "image --quiet --format json --scanners vuln --timeout 15m"
	if ignoreUnfixed {
		args += " --ignore-unfixed"
	}
	if net.TrivyDB != "" {
		args += " --db-repository " + q(net.TrivyDB)
	}
	return fmt.Sprintf("docker run --rm%s -v /var/run/docker.sock:/var/run/docker.sock -v %s:/root/.cache/ %s %s %s",
		envFlags(buildProxyEnv(net)), trivyCacheVolume, q(scanner), args, q(image))
}

// scanTarget is an image to scan, with its compose service if any.
type scanTarget struct {
	Service string
	Image   string
}

// startScans records a running scan for each target.
func (e *Engine) startScans(ctx context.Context, app store.Application, deploymentID int64, trigger string, targets []scanTarget) []store.ImageScan {
	scans := make([]store.ImageScan, 0, len(targets))
	for _, t := range targets {
		s := store.ImageScan{ApplicationID: app.ID, DeploymentID: deploymentID, Service: t.Service, Image: t.Image, Trigger: trigger, Status: "running", CreatedAt: store.Now()}
		if _, err := store.Insert(ctx, e.db, "image_scans", &s); err == nil {
			scans = append(scans, s)
		}
	}
	return scans
}

// runScans scans the images one after the other on x (at most two scans run
// at a time on PortDock) and stores the findings.
func (e *Engine) runScans(ctx context.Context, x executor.Executor, app store.Application, scans []store.ImageScan, log *logger) []store.ImageScan {
	scanner := e.trivyImage(ctx)
	for i := range scans {
		s := &scans[i]
		select {
		case e.scanSlots <- struct{}{}:
		case <-ctx.Done():
			s.Status, s.Error = "failed", "cancelled"
			e.finishScan(context.WithoutCancel(ctx), s, nil)
			continue
		}
		start := time.Now()
		if log != nil {
			label := s.Image
			if s.Service != "" {
				label = s.Service + " (" + s.Image + ")"
			}
			log.Printf("Scanning %s with %s…", label, scanner)
		}
		out, err := executor.Output(ctx, x, trivyCmd(scanner, s.Image, app.ScanIgnoreUnfixed, e.Network(ctx)))
		<-e.scanSlots
		var fs []Finding
		if err == nil {
			fs, err = ParseTrivy([]byte(out))
		}
		if err != nil {
			s.Status, s.Error = "failed", truncateErr(err.Error(), 2000)
		} else {
			s.Status = "success"
			countFindings(s, fs)
		}
		e.finishScan(context.WithoutCancel(ctx), s, fs)
		if log != nil {
			if err != nil {
				log.Printf("⚠ Scan failed: %s", s.Error)
			} else {
				log.Printf("%d critical · %d high · %d medium · %d low (%d with a fix) in %s",
					s.Critical, s.High, s.Medium, s.Low, s.Fixable, time.Since(start).Round(time.Second))
			}
		}
		if err == nil && s.Critical+s.High > 0 {
			e.notifyVulnerable(app, *s)
		}
	}
	e.pruneScans(context.WithoutCancel(ctx), app.ID)
	return scans
}

func truncateErr(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func (e *Engine) finishScan(ctx context.Context, s *store.ImageScan, fs []Finding) {
	if len(fs) > maxFindings {
		fs = fs[:maxFindings]
	}
	if fs != nil {
		b, _ := json.Marshal(fs)
		s.Findings = string(b)
	}
	s.FinishedAt = store.Now()
	if err := store.Update(ctx, e.db, "image_scans", s); err != nil {
		slog.Warn("save scan", "err", err)
	}
}

func (e *Engine) pruneScans(ctx context.Context, appID int64) {
	_, _ = e.db.ExecContext(ctx, `DELETE FROM image_scans WHERE application_id = ? AND status != 'running' AND id NOT IN
		(SELECT id FROM image_scans WHERE application_id = ? ORDER BY id DESC LIMIT ?)`, appID, appID, scansPerApp)
}

func (e *Engine) notifyVulnerable(app store.Application, s store.ImageScan) {
	if e.notify == nil {
		return
	}
	image := s.Image
	if s.Service != "" {
		image = s.Service + " · " + s.Image
	}
	e.notify.Send(notify.Event{
		Name:    "security.vulnerabilities",
		Title:   fmt.Sprintf("%s: %d critical and %d high vulnerabilities", app.Name, s.Critical, s.High),
		Message: image,
		URL:     fmt.Sprintf("%s/applications/%d?tab=security", e.PublicURL(context.Background()), app.ID),
		Fields:  map[string]string{"Critical": fmt.Sprint(s.Critical), "High": fmt.Sprint(s.High), "With a fix": fmt.Sprint(s.Fixable)},
	})
}

// scanBeforeRollout scans a freshly built or pulled image and stops the
// deployment when the policy of the application blocks it. A scan that
// cannot run is reported and does not stop the deployment.
func (j *job) scanBeforeRollout(ctx context.Context, x executor.Executor, image string) error {
	j.log.Step("Vulnerability scan (Trivy)")
	scans := j.e.startScans(ctx, j.app, j.d.ID, "deploy", []scanTarget{{Image: image}})
	scans = j.e.runScans(ctx, x, j.app, scans, j.log)
	for _, s := range scans {
		if s.Status != "success" {
			j.log.Printf("⚠ The image was not scanned; deploying anyway.")
			continue
		}
		if ScanBlocks(j.app.ScanBlock, s) {
			return fmt.Errorf("deployment blocked by the security policy: %d critical and %d high vulnerabilities (see the Security tab)", s.Critical, s.High)
		}
	}
	return nil
}

// composeImages lists the image of every running service of a compose application.
func composeImages(ctx context.Context, x executor.Executor, app store.Application) ([]scanTarget, error) {
	out, err := executor.Output(ctx, x, fmt.Sprintf(`docker ps --filter label=com.docker.compose.project=%s --format '{{.Label "com.docker.compose.service"}}|{{.Image}}'`,
		q(composeProject(app.UUID))))
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var ts []scanTarget
	for _, line := range strings.Split(out, "\n") {
		svc, img, ok := strings.Cut(strings.TrimSpace(line), "|")
		if !ok || img == "" || seen[svc] {
			continue
		}
		seen[svc] = true
		ts = append(ts, scanTarget{Service: svc, Image: img})
	}
	sort.Slice(ts, func(i, k int) bool { return ts[i].Service < ts[k].Service })
	return ts, nil
}

// ErrScanRunning is returned when a scan of the application is already running.
var ErrScanRunning = errors.New("a scan of this application is already running")

// ScanApp scans the images the application runs on its primary server, in the
// background. deploymentID and trigger describe what started it.
func (e *Engine) ScanApp(ctx context.Context, app store.Application, deploymentID int64, trigger string) error {
	var n int
	_ = e.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM image_scans WHERE application_id = ? AND status = 'running'", app.ID).Scan(&n)
	if n > 0 {
		return ErrScanRunning
	}
	x, _, err := e.pool.ForID(ctx, app.ServerID)
	if err != nil {
		return err
	}
	var targets []scanTarget
	if app.BuildPack == "compose" {
		if targets, err = composeImages(ctx, x, app); err != nil {
			return err
		}
	} else if app.CurrentImage != "" {
		targets = []scanTarget{{Image: app.CurrentImage}}
	}
	if len(targets) == 0 {
		return errors.New("the application is not running: deploy it first")
	}
	scans := e.startScans(ctx, app, deploymentID, trigger, targets)
	go func() {
		bg, cancel := context.WithTimeout(context.Background(), time.Hour)
		defer cancel()
		e.runScans(bg, x, app, scans, nil)
	}()
	return nil
}

// RecoverScans marks scans that a restart of PortDock interrupted as failed.
func (e *Engine) RecoverScans(ctx context.Context) {
	_, _ = e.db.ExecContext(ctx, "UPDATE image_scans SET status = 'failed', error = 'interrupted: PortDock restarted', finished_at = ? WHERE status = 'running'", store.Now())
}

// ScanFindings decodes the stored findings of a scan.
func ScanFindings(s store.ImageScan) []Finding {
	var fs []Finding
	_ = json.Unmarshal([]byte(s.Findings), &fs)
	if fs == nil {
		fs = []Finding{}
	}
	return fs
}
