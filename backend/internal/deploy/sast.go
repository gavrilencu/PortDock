package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/netcfg"
	"github.com/portdock/portdock/internal/store"
)

// Static application security testing runs Semgrep (https://semgrep.dev) on
// the source of the application: after the checkout of each deployment, where
// it can block the deployment, or on demand on the latest commit of the branch.

const (
	DefaultSemgrepImage  = "semgrep/semgrep:latest"
	DefaultSemgrepConfig = "p/default"
)

func (e *Engine) semgrepImage(ctx context.Context) string {
	if v := strings.TrimSpace(e.db.Setting(ctx, "semgrep_image")); v != "" {
		return v
	}
	return DefaultSemgrepImage
}

// semgrepConfigs are the rule sets: registry names (p/default, p/owasp-top-ten),
// URLs or files of the repository, comma separated.
func (e *Engine) semgrepConfigs(ctx context.Context) []string {
	v := strings.TrimSpace(e.db.Setting(ctx, "semgrep_config"))
	if v == "" {
		v = DefaultSemgrepConfig
	}
	return store.SplitList(v)
}

func semgrepCmd(image string, configs []string, dir string, net netcfg.Config) string {
	var flags strings.Builder
	for _, c := range configs {
		flags.WriteString(" --config " + q(c))
	}
	return fmt.Sprintf("docker run --rm%s -v %s:/src:ro -w /src %s semgrep scan%s --sarif --quiet --metrics off --disable-version-check --timeout 30 --max-target-bytes 1000000 /src",
		envFlags(buildProxyEnv(net)), q(dir), q(image), flags.String())
}

var cweRe = regexp.MustCompile(`(?i)\bCWE-(\d+)`)

// ParseSARIF reads the findings of a SARIF report (Semgrep and other SAST tools).
func ParseSARIF(raw []byte) ([]Finding, error) {
	var rep struct {
		Runs []struct {
			Tool struct {
				Driver struct {
					Rules []struct {
						ID               string `json:"id"`
						ShortDescription struct {
							Text string `json:"text"`
						} `json:"shortDescription"`
						FullDescription struct {
							Text string `json:"text"`
						} `json:"fullDescription"`
						Help struct {
							Text string `json:"text"`
						} `json:"help"`
						HelpURI              string `json:"helpUri"`
						DefaultConfiguration struct {
							Level string `json:"level"`
						} `json:"defaultConfiguration"`
						Properties struct {
							Tags []string `json:"tags"`
						} `json:"properties"`
					} `json:"rules"`
				} `json:"driver"`
			} `json:"tool"`
			Results []struct {
				RuleID  string `json:"ruleId"`
				Level   string `json:"level"`
				Message struct {
					Text string `json:"text"`
				} `json:"message"`
				Locations []struct {
					PhysicalLocation struct {
						ArtifactLocation struct {
							URI string `json:"uri"`
						} `json:"artifactLocation"`
						Region struct {
							StartLine int `json:"startLine"`
						} `json:"region"`
					} `json:"physicalLocation"`
				} `json:"locations"`
			} `json:"results"`
		} `json:"runs"`
	}
	if i := strings.IndexByte(string(raw), '{'); i > 0 {
		raw = raw[i:]
	}
	if err := json.Unmarshal(raw, &rep); err != nil {
		return nil, fmt.Errorf("unexpected SARIF report: %w", err)
	}
	out := []Finding{}
	for _, run := range rep.Runs {
		type rule struct{ level, desc, help, url, cwe string }
		rules := map[string]rule{}
		for _, r := range run.Tool.Driver.Rules {
			cwe := ""
			for _, t := range r.Properties.Tags {
				if m := cweRe.FindStringSubmatch(t); m != nil {
					cwe = "CWE-" + m[1]
					break
				}
			}
			desc := r.FullDescription.Text
			if desc == "" {
				desc = r.ShortDescription.Text
			}
			rules[r.ID] = rule{level: r.DefaultConfiguration.Level, desc: desc, help: r.Help.Text, url: r.HelpURI, cwe: cwe}
		}
		for _, res := range run.Results {
			r := rules[res.RuleID]
			level := res.Level
			if level == "" {
				level = r.level
			}
			f := Finding{ID: res.RuleID, Severity: sarifSeverity(level), Title: strings.TrimSpace(res.Message.Text), URL: r.url, CWE: r.cwe, Description: r.desc}
			if r.help != "" && r.help != r.desc {
				f.Solution = r.help
			}
			if len(res.Locations) > 0 {
				loc := res.Locations[0].PhysicalLocation
				f.Target = strings.TrimPrefix(strings.TrimPrefix(loc.ArtifactLocation.URI, "file://"), "/src/")
				f.Line = loc.Region.StartLine
			}
			out = append(out, f)
		}
	}
	sortFindings(out)
	return out, nil
}

func sarifSeverity(level string) string {
	switch strings.ToLower(level) {
	case "error":
		return "HIGH"
	case "warning":
		return "MEDIUM"
	case "note":
		return "LOW"
	}
	return "INFO"
}

// startScan records a running scan of one kind.
func (e *Engine) startScan(ctx context.Context, s store.ImageScan) (store.ImageScan, error) {
	s.Status, s.CreatedAt = "running", store.Now()
	_, err := store.Insert(ctx, e.db, "image_scans", &s)
	return s, err
}

// runSAST scans the source in dir on x and stores the findings and reports.
func (e *Engine) runSAST(ctx context.Context, x executor.Executor, app store.Application, s *store.ImageScan, dir string, log *logger) {
	select {
	case e.scanSlots <- struct{}{}:
	case <-ctx.Done():
		s.Status, s.Error = "failed", "cancelled"
		e.finishScan(context.WithoutCancel(ctx), s, nil)
		return
	}
	start := time.Now()
	image := e.semgrepImage(ctx)
	if log != nil {
		log.Printf("Scanning the source with %s (rules: %s)…", image, strings.Join(e.semgrepConfigs(ctx), ", "))
	}
	out, err := executor.Output(ctx, x, semgrepCmd(image, e.semgrepConfigs(ctx), dir, e.Network(ctx)))
	<-e.scanSlots
	var fs []Finding
	if err == nil || strings.Contains(out, `"runs"`) { // findings can come with a non-zero exit
		fs, err = ParseSARIF([]byte(out))
	}
	if err != nil {
		s.Status, s.Error = "failed", truncateErr(err.Error(), 2000)
	} else {
		s.Status = "success"
		countFindings(s, fs)
		s.FinishedAt = store.Now()
		e.saveReports(ctx, app, s, fs, map[string][]byte{"semgrep.sarif": []byte(out)})
	}
	e.finishScan(context.WithoutCancel(ctx), s, fs)
	if log != nil {
		if err != nil {
			log.Printf("⚠ Static analysis failed: %s", s.Error)
		} else {
			log.Printf("%d high · %d medium · %d low findings in %s", s.High, s.Medium, s.Low, time.Since(start).Round(time.Second))
		}
	}
	if err == nil && s.Critical+s.High > 0 {
		e.notifyFindings(app, *s)
	}
	e.pruneScans(context.WithoutCancel(ctx), app.ID, "sast")
}

// sastAfterCheckout scans the checkout of a deployment and stops it when the
// policy of the application blocks it. A scan that can't run doesn't stop it.
func (j *job) sastAfterCheckout(ctx context.Context) error {
	j.log.Step("Static analysis (SAST, Semgrep)")
	s, err := j.e.startScan(ctx, store.ImageScan{ApplicationID: j.app.ID, DeploymentID: j.d.ID, Kind: "sast", Trigger: "deploy", CommitSHA: j.d.CommitSHA})
	if err != nil {
		return err
	}
	j.e.runSAST(ctx, j.x, j.app, &s, j.ctxDir, j.log)
	if s.Status != "success" {
		j.log.Printf("⚠ The source was not scanned; deploying anyway.")
		return nil
	}
	if ScanBlocks(j.app.SastBlock, s) {
		return fmt.Errorf("deployment blocked by the security policy: %d high static analysis findings (see the Security tab)", s.High)
	}
	return nil
}

// ErrNoSource is returned for applications deployed from an image.
var ErrNoSource = errors.New("static analysis needs the source: this application is deployed from an image")

// ScanSource clones the branch of an application on its primary server and
// scans it in the background.
func (e *Engine) ScanSource(ctx context.Context, app store.Application) error {
	if app.SourceType == "image" || app.BuildPack == "image" {
		return ErrNoSource
	}
	if e.scanRunning(ctx, app.ID, "sast") {
		return ErrScanRunning
	}
	x, _, err := e.pool.ForID(ctx, app.ServerID)
	if err != nil {
		return err
	}
	s, err := e.startScan(ctx, store.ImageScan{ApplicationID: app.ID, Kind: "sast", Trigger: "manual"})
	if err != nil {
		return err
	}
	go func() {
		bg, cancel := context.WithTimeout(context.Background(), time.Hour)
		defer cancel()
		dir := path.Join(e.cfg.DataDir, "sast", fmt.Sprintf("%s-%d", app.UUID, s.ID))
		log := newLogger(e, 0)
		defer log.Close()
		j := &job{e: e, app: app, x: x, log: log, workdir: dir, srcDir: dir + "/src", net: e.Network(bg),
			d: &store.Deployment{Branch: app.GitBranch}}
		var perr error
		if j.ctxDir, perr = subPath(j.srcDir, app.BaseDirectory); perr != nil {
			s.Status, s.Error = "failed", perr.Error()
			e.finishScan(context.WithoutCancel(bg), &s, nil)
			return
		}
		defer func() {
			parent, name := path.Split(dir)
			_ = x.Run(context.WithoutCancel(bg), fmt.Sprintf("rm -rf %s 2>/dev/null || docker run --rm -v %s:/w alpine rm -rf /w/%s", q(dir), q(parent), q(name)), executor.RunOpts{})
		}()
		if err := j.checkout(bg); err != nil {
			s.Status, s.Error = "failed", truncateErr(err.Error(), 2000)
			e.finishScan(context.WithoutCancel(bg), &s, nil)
			return
		}
		s.CommitSHA = j.d.CommitSHA
		e.runSAST(bg, x, app, &s, j.ctxDir, nil)
	}()
	return nil
}

func (e *Engine) scanRunning(ctx context.Context, appID int64, kind string) bool {
	var n int
	_ = e.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM image_scans WHERE application_id = ? AND kind = ? AND status = 'running'", appID, kind).Scan(&n)
	return n > 0
}
