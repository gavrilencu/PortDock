package docker

import (
	"strings"
	"testing"
)

func TestLegacyLabels(t *testing.T) {
	if Legacy(LabelResource) != "shipyard.resource" {
		t.Fatal(Legacy(LabelResource))
	}
	l := NormalizeLabels(map[string]string{"shipyard.resource": "abc", "shipyard.pr": "0", "portdock.kind": "application"})
	if l[LabelResource] != "abc" || l[LabelPR] != "0" || l[LabelKind] != "application" {
		t.Fatalf("%v", l)
	}
	cmd := IDsCmd("ps", true, "label="+LabelResource+"=abc", "label="+LabelPR+"=0")
	if !strings.Contains(cmd, "label=portdock.resource=abc") || !strings.Contains(cmd, "label=shipyard.resource=abc") || !strings.Contains(cmd, "sort -u") || !strings.Contains(cmd, "docker ps -qa") {
		t.Fatal(cmd)
	}
	if IDsCmd("ps", false, "status=exited") != "docker ps -q --filter status=exited" {
		t.Fatal(IDsCmd("ps", false, "status=exited"))
	}
}
