// Package notify sends event notifications to Discord, Slack, Microsoft Teams, Telegram, email or generic webhooks.
package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/smtp"
	"slices"
	"strings"
	"time"

	"github.com/portdock/portdock/internal/secrets"
	"github.com/portdock/portdock/internal/store"
)

type Event struct {
	Name    string            `json:"event"` // e.g. deployment.failed
	Title   string            `json:"title"`
	Message string            `json:"message"`
	URL     string            `json:"url,omitempty"`
	Fields  map[string]string `json:"fields,omitempty"`
	Success bool              `json:"success"`
}

type Notifier struct {
	db     *store.DB
	box    *secrets.Box
	client *http.Client
}

func New(db *store.DB, box *secrets.Box) *Notifier {
	return &Notifier{db: db, box: box, client: &http.Client{Timeout: 15 * time.Second}}
}

// Send dispatches ev asynchronously to every enabled channel subscribed to it.
func (n *Notifier) Send(ev Event) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		chans, err := store.Select[store.NotificationChannel](ctx, n.db, "SELECT * FROM notification_channels WHERE enabled = 1")
		if err != nil {
			return
		}
		for _, c := range chans {
			if !slices.Contains(store.SplitList(c.Events), ev.Name) {
				continue
			}
			if err := n.SendTo(ctx, c, ev); err != nil {
				slog.Warn("notification failed", "channel", c.Name, "err", err)
			}
		}
	}()
}

// SendTo delivers ev to a single channel synchronously (used for "test" too).
func (n *Notifier) SendTo(ctx context.Context, c store.NotificationChannel, ev Event) error {
	cfg := map[string]string{}
	if raw := n.box.MustDecrypt(c.Config); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg)
	}
	text := ev.Title
	if ev.Message != "" {
		text += "\n" + ev.Message
	}
	for k, v := range ev.Fields {
		text += fmt.Sprintf("\n• %s: %s", k, v)
	}
	if ev.URL != "" {
		text += "\n" + ev.URL
	}
	switch c.Type {
	case "discord":
		color := 0xE5484D
		if ev.Success {
			color = 0x30A46C
		}
		var fields []map[string]any
		for k, v := range ev.Fields {
			fields = append(fields, map[string]any{"name": k, "value": v, "inline": true})
		}
		return n.postJSON(ctx, cfg["webhook_url"], map[string]any{
			"username": "PortDock",
			"embeds": []map[string]any{{
				"title": ev.Title, "description": ev.Message, "url": ev.URL, "color": color, "fields": fields,
				"timestamp": time.Now().UTC().Format(time.RFC3339),
			}},
		}, nil)
	case "slack":
		return n.postJSON(ctx, cfg["webhook_url"], map[string]any{"text": text}, nil)
	case "teams":
		return n.postJSON(ctx, cfg["webhook_url"], teamsCard(ev), nil)
	case "telegram":
		url := "https://api.telegram.org/bot" + cfg["bot_token"] + "/sendMessage"
		return n.postJSON(ctx, url, map[string]any{"chat_id": cfg["chat_id"], "text": text, "disable_web_page_preview": true}, nil)
	case "webhook":
		body, _ := json.Marshal(ev)
		headers := map[string]string{}
		if s := cfg["secret"]; s != "" {
			m := hmac.New(sha256.New, []byte(s))
			m.Write(body)
			headers["X-PortDock-Signature"] = "sha256=" + hex.EncodeToString(m.Sum(nil))
			headers["X-Shipyard-Signature"] = headers["X-PortDock-Signature"] // pre-rename receivers
		}
		return n.post(ctx, cfg["url"], body, headers)
	case "email":
		return sendMail(cfg, ev.Title, text)
	}
	return fmt.Errorf("unknown channel type %q", c.Type)
}

// teamsCard is an Adaptive Card message, accepted by Teams Workflows ("Post to a
// channel when a webhook request is received") and by incoming webhooks.
func teamsCard(ev Event) map[string]any {
	color := "Attention"
	if ev.Success {
		color = "Good"
	}
	body := []map[string]any{
		{"type": "TextBlock", "text": ev.Title, "weight": "Bolder", "size": "Medium", "color": color, "wrap": true},
	}
	if ev.Message != "" {
		body = append(body, map[string]any{"type": "TextBlock", "text": ev.Message, "wrap": true})
	}
	if len(ev.Fields) > 0 {
		keys := make([]string, 0, len(ev.Fields))
		for k := range ev.Fields {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		facts := make([]map[string]string, 0, len(keys))
		for _, k := range keys {
			facts = append(facts, map[string]string{"title": k, "value": ev.Fields[k]})
		}
		body = append(body, map[string]any{"type": "FactSet", "facts": facts})
	}
	card := map[string]any{
		"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
		"type":    "AdaptiveCard", "version": "1.4", "body": body,
		"msteams": map[string]any{"width": "Full"},
	}
	if ev.URL != "" {
		card["actions"] = []map[string]any{{"type": "Action.OpenUrl", "title": "Open in PortDock", "url": ev.URL}}
	}
	return map[string]any{
		"type":        "message",
		"summary":     ev.Title,
		"attachments": []map[string]any{{"contentType": "application/vnd.microsoft.card.adaptive", "content": card}},
	}
}

func (n *Notifier) postJSON(ctx context.Context, url string, payload any, headers map[string]string) error {
	body, _ := json.Marshal(payload)
	return n.post(ctx, url, body, headers)
}

func (n *Notifier) post(ctx context.Context, url string, body []byte, headers map[string]string) error {
	if url == "" {
		return fmt.Errorf("missing url")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("http %d", res.StatusCode)
	}
	return nil
}

func sendMail(cfg map[string]string, subject, body string) error {
	host, port := cfg["host"], cfg["port"]
	if port == "" {
		port = "587"
	}
	to := store.SplitList(cfg["to"])
	if host == "" || len(to) == 0 {
		return fmt.Errorf("smtp host and recipients are required")
	}
	var auth smtp.Auth
	if cfg["username"] != "" {
		auth = smtp.PlainAuth("", cfg["username"], cfg["password"], host)
	}
	from := cfg["from"]
	if from == "" {
		from = cfg["username"]
	}
	msg := "From: " + from + "\r\nTo: " + strings.Join(to, ",") + "\r\nSubject: [PortDock] " + subject +
		"\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" + body
	return smtp.SendMail(host+":"+port, auth, from, to, []byte(msg))
}
