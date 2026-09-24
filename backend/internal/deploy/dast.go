package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/store"
)

// Dynamic application security testing runs the OWASP ZAP baseline scan
// (https://www.zaproxy.org/docs/docker/baseline-scan/) against the running
// application: it spiders the site and reports what passive rules find
// (missing security headers, cookies, information leaks…). It doesn't attack
// the application, so it is safe for production. It runs on the server of the
// application, on the proxy network, after a deployment or on demand.

const DefaultZAPImage = "ghcr.io/zaproxy/zaproxy:stable"

func (e *Engine) zapImage(ctx context.Context) string {
	if v := strings.TrimSpace(e.db.Setting(ctx, "zap_image")); v != "" {
		return v
	}
	return DefaultZAPImage
}

// DASTTarget is the URL a DAST scan of app requests: the configured URL, or the
// application itself inside the proxy network (single-container applications),
// or its first domain.
func (e *Engine) DASTTarget(ctx context.Context, app store.Application) (string, error) {
	if t := strings.TrimSpace(app.DastTarget); t != "" {
		return t, nil
	}
	if app.BuildPack != "compose" {
		port := app.Port
		if app.BuildPack == "static" || port == 0 {
			port = 80
		}
		return fmt.Sprintf("http://%s:%d/", app.UUID, port), nil
	}
	routes, err := e.DomainRoutes(ctx, app)
	if err == nil && len(routes) > 0 {
		return urlOf(routes[0]), nil
	}
	return "", errors.New("add a domain or set the URL to scan in the DAST settings of this Docker Compose application")
}

var riskSeverity = map[string]string{"3": "HIGH", "2": "MEDIUM", "1": "LOW", "0": "INFO"}

var linkRe = regexp.MustCompile(`https?://[^\s<>"]+`)

// ParseZAP reads the JSON report of a ZAP scan.
func ParseZAP(raw []byte) ([]Finding, error) {
	var rep struct {
		Site []struct {
			Name   string `json:"@name"`
			Alerts []struct {
				PluginID  string `json:"pluginid"`
				Alert     string `json:"alert"`
				Name      string `json:"name"`
				RiskCode  string `json:"riskcode"`
				Desc      string `json:"desc"`
				Solution  string `json:"solution"`
				Reference string `json:"reference"`
				CWEID     string `json:"cweid"`
				Count     string `json:"count"`
				Instances []struct {
					URI    string `json:"uri"`
					Method string `json:"method"`
				} `json:"instances"`
			} `json:"alerts"`
		} `json:"site"`
	}
	if err := json.Unmarshal(raw, &rep); err != nil {
		return nil, fmt.Errorf("unexpected ZAP report: %w", err)
	}
	out := []Finding{}
	for _, site := range rep.Site {
		for _, a := range site.Alerts {
			f := Finding{ID: a.PluginID, Severity: riskSeverity[a.RiskCode], Title: a.Alert, Description: plainText(a.Desc), Solution: plainText(a.Solution)}
			if f.Title == "" {
				f.Title = a.Name
			}
			if f.Severity == "" {
				f.Severity = "INFO"
			}
			if n, err := strconv.Atoi(a.CWEID); err == nil && n > 0 {
				f.CWE = "CWE-" + a.CWEID
			}
			f.Count, _ = strconv.Atoi(a.Count)
			if f.Count == 0 {
				f.Count = len(a.Instances)
			}
			if len(a.Instances) > 0 {
				f.Target = a.Instances[0].URI
			} else {
				f.Target = site.Name
			}
			if m := linkRe.FindString(a.Reference); m != "" {
				f.URL = m
			} else if f.ID != "" {
				f.URL = "https://www.zaproxy.org/docs/alerts/" + f.ID + "/"
			}
			out = append(out, f)
		}
	}
	sortFindings(out)
	return out, nil
}

// runDAST runs the ZAP baseline scan on x and stores the findings and reports.
func (e *Engine) runDAST(ctx context.Context, x executor.Executor, app store.Application, s *store.ImageScan) {
	select {
	case e.scanSlots <- struct{}{}:
	case <-ctx.Done():
		s.Status, s.Error = "failed", "cancelled"
		e.finishScan(context.WithoutCancel(ctx), s, nil)
		return
	}
	defer func() { <-e.scanSlots }()
	// ZAP runs as user zap (uid 1000) and writes its reports into /zap/wrk.
	dir := path.Join(e.cfg.DataDir, "dast", strconv.FormatInt(s.ID, 10))
	cleanup := func() {
		parent, name := path.Split(dir)
		_ = x.Run(context.WithoutCancel(ctx), fmt.Sprintf("rm -rf %s 2>/dev/null || docker run --rm -v %s:/w alpine rm -rf /w/%s", q(dir), q(parent), q(name)), executor.RunOpts{})
	}
	defer cleanup()
	if err := x.Run(ctx, fmt.Sprintf("mkdir -p %s && chmod 777 %s", q(dir), q(dir)), executor.RunOpts{}); err != nil {
		s.Status, s.Error = "failed", err.Error()
		e.finishScan(context.WithoutCancel(ctx), s, nil)
		return
	}
	// -I: warnings don't make the scan fail; -m: minutes of spidering; -T: overall limit.
	cmd := fmt.Sprintf("docker run --rm --network %s -v %s:/zap/wrk:rw %s zap-baseline.py -t %s -J zap.json -r zap.html -I -m 2 -T 15",
		q(e.cfg.ProxyNetwork), q(dir), q(e.zapImage(ctx)), q(s.Target))
	runOut, runErr := executor.Output(ctx, x, cmd)
	jsonOut, jerr := executor.Output(ctx, x, "cat "+q(dir+"/zap.json"))
	var fs []Finding
	err := jerr
	if jerr == nil {
		fs, err = ParseZAP([]byte(jsonOut))
	}
	if err != nil {
		msg := runOut
		if runErr != nil {
			msg = runErr.Error()
		}
		s.Status, s.Error = "failed", truncateErr(lastLines(msg, 15), 2000)
		e.finishScan(context.WithoutCancel(ctx), s, nil)
		return
	}
	htmlOut, _ := executor.Output(ctx, x, "cat "+q(dir+"/zap.html"))
	s.Status = "success"
	countFindings(s, fs)
	s.FinishedAt = store.Now()
	e.saveReports(ctx, app, s, fs, map[string][]byte{"zap.json": []byte(jsonOut), "zap.html": []byte(htmlOut)})
	e.finishScan(context.WithoutCancel(ctx), s, fs)
	if s.High > 0 {
		e.notifyFindings(app, *s)
	}
	e.pruneScans(context.WithoutCancel(ctx), app.ID, "dast")
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// ScanRunning starts a DAST scan of the running application in the background.
func (e *Engine) ScanRunning(ctx context.Context, app store.Application, deploymentID int64, trigger string) error {
	if e.scanRunning(ctx, app.ID, "dast") {
		return ErrScanRunning
	}
	if app.Status != "running" && trigger == "manual" {
		return errors.New("the application is not running: deploy it first")
	}
	target, err := e.DASTTarget(ctx, app)
	if err != nil {
		return err
	}
	x, _, err := e.pool.ForID(ctx, app.ServerID)
	if err != nil {
		return err
	}
	s, err := e.startScan(ctx, store.ImageScan{ApplicationID: app.ID, DeploymentID: deploymentID, Kind: "dast", Trigger: trigger, Target: target})
	if err != nil {
		return err
	}
	go func() {
		bg, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		e.runDAST(bg, x, app, &s)
	}()
	return nil
}
