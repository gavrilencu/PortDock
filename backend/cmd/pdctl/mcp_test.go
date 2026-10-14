package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestMCPBridge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/mcp" || r.Method != "POST" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		switch {
		case r.Header.Get("Authorization") != "Bearer pd_good":
			w.WriteHeader(401)
			w.Write([]byte(`{"error":"invalid token"}`))
		case bytes.Contains(b, []byte(`"slow"`)):
			time.Sleep(300 * time.Millisecond)
			w.Write([]byte("{\n  \"jsonrpc\": \"2.0\",\n  \"id\": 1,\n  \"result\": {}\n}\n"))
		case !bytes.Contains(b, []byte(`"id"`)):
			w.WriteHeader(202)
		default:
			var m struct{ ID json.RawMessage }
			json.Unmarshal(b, &m)
			w.Write([]byte(`{"jsonrpc":"2.0","id":` + string(m.ID) + `,"result":{}}` + "\n"))
		}
	}))
	defer srv.Close()

	run := func(token, in string) []string {
		var out bytes.Buffer
		c := &client{url: srv.URL, token: token}
		if err := c.mcpBridge(strings.NewReader(in), &out); err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(out.String()), "\n")
		sort.Strings(lines)
		return lines
	}

	// The slow request does not hold up the next one, the notification gets
	// no answer and a pretty-printed answer stays on one line.
	got := run("pd_good", `{"jsonrpc":"2.0","id":1,"method":"slow"}`+"\n\n"+
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n"+
		`{"jsonrpc":"2.0","id":"b","method":"ping"}`+"\n")
	want := []string{`{"jsonrpc":"2.0","id":"b","result":{}}`, `{"jsonrpc":"2.0","id":1,"result":{}}`}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("answers:\n%s", strings.Join(got, "\n"))
	}

	// A refused token becomes a JSON-RPC error for each request.
	got = run("pd_bad", `{"jsonrpc":"2.0","id":7,"method":"ping"}`+"\n"+
		`[{"jsonrpc":"2.0","id":8,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/initialized"}]`+"\n")
	if len(got) != 2 || !strings.Contains(got[0], `"id":8`) || !strings.HasPrefix(got[0], "[") ||
		!strings.Contains(got[1], `"id":7`) || !strings.Contains(got[1], "invalid token (HTTP 401)") {
		t.Errorf("errors:\n%s", strings.Join(got, "\n"))
	}
}
