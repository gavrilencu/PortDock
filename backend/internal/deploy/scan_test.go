package deploy

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/portdock/portdock/internal/executor"
	"github.com/portdock/portdock/internal/netcfg"
	"github.com/portdock/portdock/internal/store"
)

const trivySample = `2024-01-01T00:00:00Z INFO Need to update DB
{
  "SchemaVersion": 2,
  "ArtifactName": "web:1",
  "Results": [
    {"Target": "web:1 (alpine 3.18.0)", "Class": "os-pkgs", "Vulnerabilities": [
      {"VulnerabilityID": "CVE-2023-0001", "PkgName": "openssl", "InstalledVersion": "3.1.0", "FixedVersion": "3.1.1", "Severity": "HIGH", "Title": "a", "PrimaryURL": "https://avd.aquasec.com/nvd/cve-2023-0001"},
      {"VulnerabilityID": "CVE-2023-0002", "PkgName": "busybox", "InstalledVersion": "1.36", "FixedVersion": "", "Severity": "LOW"},
      {"VulnerabilityID": "CVE-2023-0001", "PkgName": "openssl", "InstalledVersion": "3.1.0", "FixedVersion": "3.1.1", "Severity": "HIGH"}
    ]},
    {"Target": "app/package-lock.json", "Class": "lang-pkgs", "Vulnerabilities": [
      {"VulnerabilityID": "CVE-2024-9999", "PkgName": "lodash", "InstalledVersion": "4.17.0", "FixedVersion": "4.17.21", "Severity": "CRITICAL"},
      {"VulnerabilityID": "GHSA-xxxx", "PkgName": "left-pad", "InstalledVersion": "1.0.0", "Severity": "weird"}
    ]},
    {"Target": "Python", "Class": "lang-pkgs"}
  ]
}`

func TestParseTrivy(t *testing.T) {
	fs, err := ParseTrivy([]byte(trivySample))
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 4 {
		t.Fatalf("duplicates must be merged: %+v", fs)
	}
	if fs[0].ID != "CVE-2024-9999" || fs[0].Target != "app/package-lock.json" || fs[1].Severity != "HIGH" || fs[3].Severity != "UNKNOWN" {
		t.Fatalf("order by severity: %+v", fs)
	}
	var s store.ImageScan
	countFindings(&s, fs)
	if s.Critical != 1 || s.High != 1 || s.Low != 1 || s.Unknown != 1 || s.Fixable != 2 {
		t.Fatalf("counts: %+v", s)
	}
	if !ScanBlocks("critical", s) || !ScanBlocks("high", s) || ScanBlocks("", s) {
		t.Error("policy")
	}
	s.Critical = 0
	if ScanBlocks("critical", s) || !ScanBlocks("high", s) {
		t.Error("policy without critical findings")
	}
	if _, err := ParseTrivy([]byte("FATAL image not found")); err == nil {
		t.Error("garbage must fail")
	}
}

// TestTrivyE2E scans a real image with Docker: PORTDOCK_TRIVY_E2E=1 go test ./internal/deploy -run TrivyE2E
func TestTrivyE2E(t *testing.T) {
	if os.Getenv("PORTDOCK_TRIVY_E2E") != "1" {
		t.Skip("set PORTDOCK_TRIVY_E2E=1 to scan a real image")
	}
	os.Setenv("MSYS_NO_PATHCONV", "1") // Git Bash on Windows
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	x := executor.Local{}
	img := "alpine:3.18.0"
	if err := x.Run(ctx, "docker pull -q "+img, executor.RunOpts{}); err != nil {
		t.Fatal(err)
	}
	out, err := executor.Output(ctx, x, trivyCmd(DefaultTrivyImage, img, false, netcfg.Config{}))
	if err != nil {
		t.Fatal(err)
	}
	fs, err := ParseTrivy([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	var s store.ImageScan
	countFindings(&s, fs)
	t.Logf("%s: %d findings: %d critical, %d high, %d medium, %d low, %d fixable", img, len(fs), s.Critical, s.High, s.Medium, s.Low, s.Fixable)
	if len(fs) == 0 {
		t.Fatal("alpine 3.18.0 has known vulnerabilities")
	}
	out, err = executor.Output(ctx, x, trivyCmd(DefaultTrivyImage, img, true, netcfg.Config{}))
	if err != nil {
		t.Fatal(err)
	}
	unfixed, _ := ParseTrivy([]byte(out))
	for _, f := range unfixed {
		if f.Fixed == "" {
			t.Fatalf("--ignore-unfixed kept %+v", f)
		}
	}
}
