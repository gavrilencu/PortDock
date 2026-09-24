package notify

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTeamsCard(t *testing.T) {
	msg := teamsCard(Event{Name: "deployment.failed", Title: "shop failed", Message: "build error", URL: "https://pd.example.com/d/1",
		Fields: map[string]string{"Commit": "abc1234", "Branch": "main"}})
	b, _ := json.Marshal(msg)
	s := string(b)
	for _, want := range []string{`"type":"message"`, `"contentType":"application/vnd.microsoft.card.adaptive"`, `"type":"AdaptiveCard"`,
		`"text":"shop failed"`, `"color":"Attention"`, `"type":"FactSet"`, `"title":"Branch","value":"main"`, `"type":"Action.OpenUrl"`, `"url":"https://pd.example.com/d/1"`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in %s", want, s)
		}
	}
	// Facts are sorted, so messages are stable.
	if strings.Index(s, "Branch") > strings.Index(s, "Commit") {
		t.Error("facts not sorted")
	}
	ok, _ := json.Marshal(teamsCard(Event{Title: "deployed", Success: true}))
	if !strings.Contains(string(ok), `"color":"Good"`) || strings.Contains(string(ok), "Action.OpenUrl") || strings.Contains(string(ok), "FactSet") {
		t.Errorf("success card: %s", ok)
	}
}
