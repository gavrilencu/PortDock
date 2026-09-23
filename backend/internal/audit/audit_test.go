package audit

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDeliverWebhookAndSyslog(t *testing.T) {
	var gotSig string
	var got Entry
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotSig = r.Header.Get("X-Shipyard-Signature")
		if gotSig != Sign("topsecret", body) {
			w.WriteHeader(401)
			return
		}
		_ = json.Unmarshal(body, &got)
	}))
	defer srv.Close()

	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	tcpMsg := make(chan string, 1)
	go func() {
		c, err := tcp.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		line, _ := bufio.NewReader(c).ReadString('}')
		tcpMsg <- line
	}()

	s := New(nil, nil)
	e := Entry{ID: 7, Time: "2026-09-23T12:00:00Z", Action: "deployment.approve", Target: "api #3", UserEmail: "a@b.c", IP: "10.0.0.5"}
	cfg := Config{WebhookURL: srv.URL, WebhookSecret: "topsecret", Syslog: "udp://" + udp.LocalAddr().String()}
	if err := s.Deliver(context.Background(), cfg, e); err != nil {
		t.Fatal(err)
	}
	if got.Action != "deployment.approve" || got.IP != "10.0.0.5" || !strings.HasPrefix(gotSig, "sha256=") {
		t.Fatalf("webhook got %+v sig %s", got, gotSig)
	}
	buf := make([]byte, 2048)
	_ = udp.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, _, err := udp.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	msg := string(buf[:n])
	if !strings.HasPrefix(msg, "<110>1 2026-09-23T12:00:00Z ") || !strings.Contains(msg, " shipyard - deployment.approve - {") {
		t.Fatalf("syslog: %s", msg)
	}

	if err := s.Deliver(context.Background(), Config{Syslog: "tcp://" + tcp.Addr().String()}, e); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-tcpMsg:
		if !strings.Contains(m, " <110>1 ") { // RFC 6587 octet-counting prefix
			t.Fatalf("tcp framing: %s", m)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no tcp syslog message")
	}

	// A wrong secret is detected by the receiver.
	if err := s.Deliver(context.Background(), Config{WebhookURL: srv.URL, WebhookSecret: "wrong"}, e); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected 401, got %v", err)
	}
	if _, _, err := ParseSyslogTarget("http://x"); err == nil {
		t.Fatal("invalid syslog target accepted")
	}
}
