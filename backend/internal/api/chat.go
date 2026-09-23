package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/portdock/portdock/internal/assistant"
	"github.com/portdock/portdock/internal/auth"
	"github.com/portdock/portdock/internal/store"
)

// AI assistant: a chat with a model of the administrator's choice through
// OpenRouter, available to all users, admins only or selected users. Each
// user's conversations are kept on this instance.

const (
	chatMaxMessage    = 20_000  // characters per user message
	chatHistoryBudget = 120_000 // characters of history sent with a new message
	chatHistoryMax    = 40      // messages of history sent with a new message
	chatReplyTimeout  = 5 * time.Minute
)

var chatModelRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,199}$`)

type chatConfig struct {
	Enabled      bool
	Key          string
	Model        string
	Access       string // all | admins | selected
	UserIDs      []int64
	Instructions string
}

func (s *Server) chatConfig(ctx context.Context) chatConfig {
	c := chatConfig{
		Enabled:      s.db.Setting(ctx, "chat_enabled") == "true",
		Key:          s.box.MustDecrypt(s.db.Setting(ctx, "chat_api_key")),
		Model:        s.db.Setting(ctx, "chat_model"),
		Access:       firstNonEmpty(s.db.Setting(ctx, "chat_access"), "admins"),
		Instructions: s.db.Setting(ctx, "chat_instructions"),
	}
	_ = json.Unmarshal([]byte(s.db.Setting(ctx, "chat_user_ids")), &c.UserIDs)
	if c.UserIDs == nil {
		c.UserIDs = []int64{}
	}
	return c
}

func (c chatConfig) ready() bool { return c.Enabled && c.Key != "" && c.Model != "" }

func (c chatConfig) allows(u store.User) bool {
	if !c.ready() || u.Disabled {
		return false
	}
	switch c.Access {
	case "all":
		return true
	case "selected":
		return slices.Contains(c.UserIDs, u.ID)
	}
	return u.IsAdmin()
}

func (s *Server) chatSettingsOut(c chatConfig) map[string]any {
	return map[string]any{"enabled": c.Enabled, "api_key_set": c.Key != "", "model": c.Model, "access": c.Access, "user_ids": c.UserIDs, "instructions": c.Instructions}
}

func (s *Server) chatStatus(w http.ResponseWriter, r *http.Request) error {
	c := s.chatConfig(r.Context())
	return ok(w, map[string]any{"enabled": c.ready(), "allowed": c.allows(auth.UserFrom(r.Context())), "model": c.Model})
}

func (s *Server) getChatSettings(w http.ResponseWriter, r *http.Request) error {
	return ok(w, s.chatSettingsOut(s.chatConfig(r.Context())))
}

func (s *Server) updateChatSettings(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Enabled      *bool    `json:"enabled"`
		APIKey       *string  `json:"api_key"`
		ClearAPIKey  bool     `json:"clear_api_key"`
		Model        *string  `json:"model"`
		Access       *string  `json:"access"`
		UserIDs      *[]int64 `json:"user_ids"`
		Instructions *string  `json:"instructions"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	set := map[string]string{}
	if in.Enabled != nil {
		set["chat_enabled"] = fmt.Sprint(*in.Enabled)
	}
	if in.ClearAPIKey {
		set["chat_api_key"] = ""
	} else if in.APIKey != nil && strings.TrimSpace(*in.APIKey) != "" {
		set["chat_api_key"] = s.box.Encrypt(strings.TrimSpace(*in.APIKey))
	}
	if in.Model != nil {
		m := strings.TrimSpace(*in.Model)
		if m != "" && !chatModelRe.MatchString(m) {
			return badRequest("invalid model id; use an OpenRouter id such as openai/gpt-4o")
		}
		set["chat_model"] = m
	}
	if in.Access != nil {
		switch *in.Access {
		case "all", "admins", "selected":
			set["chat_access"] = *in.Access
		default:
			return badRequest("access must be all, admins or selected")
		}
	}
	if in.UserIDs != nil {
		ids := []int64{}
		for _, id := range *in.UserIDs {
			var n int
			_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE id = ?", id).Scan(&n)
			if n > 0 && !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
		b, _ := json.Marshal(ids)
		set["chat_user_ids"] = string(b)
	}
	if in.Instructions != nil {
		if utf8.RuneCountInString(*in.Instructions) > 8000 {
			return badRequest("instructions are limited to 8000 characters")
		}
		set["chat_instructions"] = strings.TrimSpace(*in.Instructions)
	}
	for k, v := range set {
		if err := s.db.SetSetting(ctx, k, v); err != nil {
			return err
		}
	}
	c := s.chatConfig(ctx)
	s.activity(r, "chat.settings", fmt.Sprintf("enabled=%v model=%s access=%s", c.Enabled, c.Model, c.Access))
	return ok(w, s.chatSettingsOut(c))
}

func (s *Server) chatModels(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	models, err := s.ai.Models(ctx)
	if err != nil {
		return errStatus(502, "%v", err)
	}
	return ok(w, map[string]any{"models": models})
}

// testChat checks the key (and the model with a tiny request). Unsaved values
// from the form take precedence over the saved ones.
func (s *Server) testChat(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		APIKey string `json:"api_key"`
		Model  string `json:"model"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	c := s.chatConfig(r.Context())
	key, model := firstNonEmpty(strings.TrimSpace(in.APIKey), c.Key), firstNonEmpty(strings.TrimSpace(in.Model), c.Model)
	if key == "" {
		return badRequest("enter an OpenRouter API key first")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	info, err := s.ai.Key(ctx, key)
	if err != nil {
		return badRequest("%v", err)
	}
	res := map[string]any{"ok": true, "label": info.Label, "usage": info.Usage, "limit": info.Limit, "reply": ""}
	if model != "" {
		reply, err := s.ai.Complete(ctx, key, model, []assistant.Message{{Role: "user", Content: "Reply with the single word OK."}}, 16)
		if err != nil {
			return badRequest("the key works, but model %s failed: %v", model, err)
		}
		res["reply"] = reply
	}
	return ok(w, res)
}

// chatUser returns the caller when the assistant is available to them.
func (s *Server) chatUser(r *http.Request) (store.User, chatConfig, error) {
	u := auth.UserFrom(r.Context())
	c := s.chatConfig(r.Context())
	if !c.ready() {
		return u, c, errStatus(403, "the AI assistant is not enabled")
	}
	if !c.allows(u) {
		return u, c, errStatus(403, "the AI assistant is not available for your account")
	}
	return u, c, nil
}

func (s *Server) ownConversation(r *http.Request, u store.User, id int64) (store.ChatConversation, error) {
	conv, err := store.ByID[store.ChatConversation](r.Context(), s.db, "chat_conversations", id)
	if err != nil || conv.UserID != u.ID {
		return conv, store.ErrNotFound
	}
	return conv, nil
}

func (s *Server) listChatConversations(w http.ResponseWriter, r *http.Request) error {
	u, _, err := s.chatUser(r)
	if err != nil {
		return err
	}
	cs, err := store.Select[store.ChatConversation](r.Context(), s.db, "SELECT * FROM chat_conversations WHERE user_id = ? ORDER BY updated_at DESC, id DESC LIMIT 200", u.ID)
	if err != nil {
		return err
	}
	if cs == nil {
		cs = []store.ChatConversation{}
	}
	return ok(w, cs)
}

func (s *Server) getChatConversation(w http.ResponseWriter, r *http.Request) error {
	u, _, err := s.chatUser(r)
	if err != nil {
		return err
	}
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	conv, err := s.ownConversation(r, u, id)
	if err != nil {
		return err
	}
	msgs, err := store.Select[store.ChatMessage](r.Context(), s.db, "SELECT * FROM chat_messages WHERE conversation_id = ? ORDER BY id", conv.ID)
	if err != nil {
		return err
	}
	if msgs == nil {
		msgs = []store.ChatMessage{}
	}
	return ok(w, map[string]any{"conversation": conv, "messages": msgs})
}

func (s *Server) deleteChatConversation(w http.ResponseWriter, r *http.Request) error {
	u := auth.UserFrom(r.Context()) // deleting stays possible after access was revoked
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	conv, err := s.ownConversation(r, u, id)
	if err != nil {
		return err
	}
	if err := store.Delete(r.Context(), s.db, "chat_conversations", conv.ID); err != nil {
		return err
	}
	return ok(w, map[string]bool{"ok": true})
}

var chatLanguages = map[string]string{"ro": "Romanian", "ru": "Russian"}

func (s *Server) chatSystemPrompt(u store.User, c chatConfig) string {
	lang := firstNonEmpty(chatLanguages[u.Locale], "English")
	var b strings.Builder
	b.WriteString(`You are PortDock Assistant, the assistant built into PortDock — a free, open-source, self-hosted platform (like Heroku or Coolify) that deploys applications from Git repositories or Docker images, Docker Compose stacks, managed databases (PostgreSQL, MySQL, MariaDB, MongoDB, Redis…) and one-click services on the user's own servers.
PortDock features: projects and environments; domains with automatic Let's Encrypt HTTPS through a Traefik proxy (for compose applications each domain can target a service); environment variables, per compose service, applied with "Restart with new variables"; versions (the live one and the last 5 are kept for 15 days) that can be deployed again by drag and drop in the Versions tab; deployment approvals and protected environments; metrics, alerts and a live topology; database backups; SSO and LDAP sign-in; an audit log; updates from GitHub releases in the sidebar.
Help the user run PortDock and their applications: explain features, troubleshoot builds, deployments, Docker, networking, DNS and TLS, and write configuration such as Dockerfiles, docker-compose.yml files and CI pipelines.
Be concise and practical and use Markdown with fenced code blocks. If you are not sure how PortDock behaves, say so instead of inventing menu names. You cannot see the user's servers or data unless they paste them here.
`)
	fmt.Fprintf(&b, "You are talking to %s (role: %s). Answer in %s unless the user writes in another language.\n", firstNonEmpty(u.Name, u.Email), u.Role, lang)
	if c.Instructions != "" {
		b.WriteString("\nInstructions from the administrator of this PortDock instance:\n" + c.Instructions + "\n")
	}
	return b.String()
}

// chatHistory returns the most recent messages of a conversation that fit the budget, oldest first.
func (s *Server) chatHistory(ctx context.Context, convID int64) ([]assistant.Message, error) {
	msgs, err := store.Select[store.ChatMessage](ctx, s.db, "SELECT * FROM chat_messages WHERE conversation_id = ? ORDER BY id DESC LIMIT ?", convID, chatHistoryMax)
	if err != nil {
		return nil, err
	}
	var out []assistant.Message
	size := 0
	for _, m := range msgs {
		if m.Content == "" {
			continue
		}
		size += len(m.Content)
		if size > chatHistoryBudget && len(out) > 0 {
			break
		}
		out = append(out, assistant.Message{Role: m.Role, Content: m.Content})
	}
	slices.Reverse(out)
	return out, nil
}

func chatTitle(content string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(content), "\n")
	line = strings.Join(strings.Fields(line), " ")
	if r := []rune(line); len(r) > 60 {
		line = strings.TrimSpace(string(r[:60])) + "…"
	}
	return line
}

// sendChat stores the user's message and streams the model's reply as
// server-sent events: conversation, user, delta…, then done (or error).
func (s *Server) sendChat(w http.ResponseWriter, r *http.Request) error {
	u, c, err := s.chatUser(r)
	if err != nil {
		return err
	}
	var in struct {
		ConversationID int64  `json:"conversation_id"`
		Content        string `json:"content"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	content := strings.TrimSpace(in.Content)
	if content == "" {
		return badRequest("the message is empty")
	}
	if utf8.RuneCountInString(content) > chatMaxMessage {
		return badRequest("messages are limited to %d characters", chatMaxMessage)
	}
	ctx := r.Context()
	db := context.WithoutCancel(ctx) // messages are saved even if the browser disconnects
	var conv store.ChatConversation
	if in.ConversationID != 0 {
		if conv, err = s.ownConversation(r, u, in.ConversationID); err != nil {
			return err
		}
	} else {
		conv = store.ChatConversation{UserID: u.ID, Title: chatTitle(content), CreatedAt: store.Now(), UpdatedAt: store.Now()}
		if _, err := store.Insert(db, s.db, "chat_conversations", &conv); err != nil {
			return err
		}
	}
	userMsg := store.ChatMessage{ConversationID: conv.ID, Role: "user", Content: content, CreatedAt: store.Now()}
	if _, err := store.Insert(db, s.db, "chat_messages", &userMsg); err != nil {
		return err
	}
	history, err := s.chatHistory(db, conv.ID)
	if err != nil {
		return err
	}
	msgs := append([]assistant.Message{{Role: "system", Content: s.chatSystemPrompt(u, c)}}, history...)

	out := newSSE(w)
	_ = out.send("conversation", conv)
	_ = out.send("user", userMsg)

	// Reasoning models can think for a while before the first token: keep the
	// connection alive through proxies.
	streamCtx, cancel := context.WithTimeout(ctx, chatReplyTimeout)
	defer cancel()
	stopPing, pinged := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(pinged)
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stopPing:
				return
			case <-t.C:
				_ = out.ping()
			}
		}
	}()

	reply, err := s.ai.Stream(streamCtx, c.Key, c.Model, msgs, func(text string) error {
		return out.send("delta", map[string]string{"text": text})
	})
	close(stopPing)
	<-pinged // nothing may write to the response once the handler returns
	cancel()
	aborted := err != nil && ctx.Err() != nil // the user pressed Stop or closed the page
	_, _ = s.db.ExecContext(db, "UPDATE chat_conversations SET updated_at = ? WHERE id = ?", store.Now(), conv.ID)
	if err != nil && !aborted {
		if errors.Is(err, context.DeadlineExceeded) {
			err = fmt.Errorf("the model did not finish within %s", chatReplyTimeout)
		}
		res := map[string]any{"error": err.Error()}
		if reply != "" {
			m := store.ChatMessage{ConversationID: conv.ID, Role: "assistant", Content: reply, Model: c.Model, Error: true, CreatedAt: store.Now()}
			if _, ierr := store.Insert(db, s.db, "chat_messages", &m); ierr == nil {
				res["message"] = m
			}
		}
		_ = out.send("error", res)
		return nil
	}
	if reply == "" && !aborted {
		_ = out.send("error", map[string]any{"error": "the model returned an empty reply"})
		return nil
	}
	if reply == "" {
		return nil
	}
	m := store.ChatMessage{ConversationID: conv.ID, Role: "assistant", Content: reply, Model: c.Model, CreatedAt: store.Now()}
	if _, err := store.Insert(db, s.db, "chat_messages", &m); err != nil {
		_ = out.send("error", map[string]any{"error": err.Error()})
		return nil
	}
	_ = out.send("done", m)
	return nil
}
