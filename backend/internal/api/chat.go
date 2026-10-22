package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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
	Enabled bool
	// Provider is "openrouter" or "openai" (an OpenAI-compatible server at BaseURL).
	Provider     string
	BaseURL      string
	Key          string
	Model        string
	Access       string // all | admins | selected
	UserIDs      []int64
	Instructions string
	// Tools lets the assistant read the instance (logs, containers, deployments…)
	// with the user's own permissions; Actions lets it propose the few actions
	// (deploy, roll back, restart, back up, run a job) that run after the user
	// confirms them in the chat. Both are on unless an administrator turns them off.
	Tools   bool
	Actions bool
}

func (s *Server) chatConfig(ctx context.Context) chatConfig {
	c := chatConfig{
		Enabled:      s.db.Setting(ctx, "chat_enabled") == "true",
		Provider:     firstNonEmpty(s.db.Setting(ctx, "chat_provider"), "openrouter"),
		BaseURL:      s.db.Setting(ctx, "chat_base_url"),
		Key:          s.box.MustDecrypt(s.db.Setting(ctx, "chat_api_key")),
		Model:        s.db.Setting(ctx, "chat_model"),
		Access:       firstNonEmpty(s.db.Setting(ctx, "chat_access"), "admins"),
		Instructions: s.db.Setting(ctx, "chat_instructions"),
		Tools:        s.db.Setting(ctx, "chat_tools") != "false",
		Actions:      s.db.Setting(ctx, "chat_actions") != "false",
	}
	_ = json.Unmarshal([]byte(s.db.Setting(ctx, "chat_user_ids")), &c.UserIDs)
	if c.UserIDs == nil {
		c.UserIDs = []int64{}
	}
	return c
}

// provider is where the assistant's model runs.
func (c chatConfig) provider() assistant.Provider {
	if c.Provider == "openai" {
		return assistant.Provider{BaseURL: c.BaseURL, Key: c.Key}
	}
	return assistant.Provider{Key: c.Key}
}

// ready: a local server needs no key, OpenRouter does.
func (c chatConfig) ready() bool {
	if !c.Enabled || c.Model == "" {
		return false
	}
	if c.Provider == "openai" {
		return c.BaseURL != ""
	}
	return c.Key != ""
}

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
	return map[string]any{"enabled": c.Enabled, "provider": c.Provider, "base_url": c.BaseURL, "api_key_set": c.Key != "", "model": c.Model, "access": c.Access, "user_ids": c.UserIDs, "instructions": c.Instructions,
		"tools_enabled": c.Tools, "actions_enabled": c.Actions}
}

func (s *Server) chatStatus(w http.ResponseWriter, r *http.Request) error {
	c := s.chatConfig(r.Context())
	return ok(w, map[string]any{"enabled": c.ready(), "allowed": c.allows(auth.UserFrom(r.Context())), "model": c.Model, "tools": c.Tools, "actions": c.Tools && c.Actions})
}

func (s *Server) getChatSettings(w http.ResponseWriter, r *http.Request) error {
	return ok(w, s.chatSettingsOut(s.chatConfig(r.Context())))
}

func (s *Server) updateChatSettings(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Enabled      *bool    `json:"enabled"`
		Provider     *string  `json:"provider"`
		BaseURL      *string  `json:"base_url"`
		APIKey       *string  `json:"api_key"`
		ClearAPIKey  bool     `json:"clear_api_key"`
		Model        *string  `json:"model"`
		Access       *string  `json:"access"`
		UserIDs      *[]int64 `json:"user_ids"`
		Instructions *string  `json:"instructions"`
		Tools        *bool    `json:"tools_enabled"`
		Actions      *bool    `json:"actions_enabled"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	set := map[string]string{}
	if in.Enabled != nil {
		set["chat_enabled"] = fmt.Sprint(*in.Enabled)
	}
	if in.Tools != nil {
		set["chat_tools"] = fmt.Sprint(*in.Tools)
	}
	if in.Actions != nil {
		set["chat_actions"] = fmt.Sprint(*in.Actions)
	}
	if in.Provider != nil {
		if *in.Provider != "openrouter" && *in.Provider != "openai" {
			return badRequest("provider must be openrouter or openai")
		}
		set["chat_provider"] = *in.Provider
	}
	if in.BaseURL != nil {
		u := strings.TrimRight(strings.TrimSpace(*in.BaseURL), "/")
		if err := validAIBaseURL(u); err != nil {
			return err
		}
		set["chat_base_url"] = u
	}
	if in.ClearAPIKey {
		set["chat_api_key"] = ""
	} else if in.APIKey != nil && strings.TrimSpace(*in.APIKey) != "" {
		set["chat_api_key"] = s.box.Encrypt(strings.TrimSpace(*in.APIKey))
	}
	if in.Model != nil {
		m := strings.TrimSpace(*in.Model)
		if m != "" && !chatModelRe.MatchString(m) {
			return badRequest("invalid model id, e.g. openai/gpt-4o on OpenRouter or qwen2.5:7b on Ollama")
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
	s.activity(r, "chat.settings", fmt.Sprintf("enabled=%v model=%s access=%s tools=%v actions=%v", c.Enabled, c.Model, c.Access, c.Tools, c.Actions))
	return ok(w, s.chatSettingsOut(c))
}

// validAIBaseURL checks the address of an OpenAI-compatible server ("" allowed).
func validAIBaseURL(u string) error {
	if u == "" {
		return nil
	}
	p, err := url.Parse(u)
	if err != nil || (p.Scheme != "http" && p.Scheme != "https") || p.Host == "" || p.User != nil {
		return badRequest("the server address must be an http(s) URL such as http://ollama:11434/v1")
	}
	return nil
}

// formProvider is the provider of the settings form (unsaved values first).
func (s *Server) formProvider(c chatConfig, provider, baseURL, key string) (assistant.Provider, error) {
	if provider == "" {
		provider = c.Provider
	}
	if provider != "openai" {
		return assistant.Provider{Key: firstNonEmpty(key, c.Key)}, nil
	}
	baseURL = strings.TrimRight(firstNonEmpty(strings.TrimSpace(baseURL), c.BaseURL), "/")
	if baseURL == "" {
		return assistant.Provider{}, badRequest("enter the address of the AI server first")
	}
	if err := validAIBaseURL(baseURL); err != nil {
		return assistant.Provider{}, err
	}
	return assistant.Provider{BaseURL: baseURL, Key: firstNonEmpty(key, c.Key)}, nil
}

func (s *Server) chatModels(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	p, err := s.formProvider(s.chatConfig(ctx), r.URL.Query().Get("provider"), r.URL.Query().Get("base_url"), "")
	if err != nil {
		return err
	}
	models, err := s.ai.Models(ctx, p)
	if err != nil {
		return errStatus(502, "%v", err)
	}
	return ok(w, map[string]any{"models": models})
}

// testChat checks the key (and the model with a tiny request). Unsaved values
// from the form take precedence over the saved ones.
func (s *Server) testChat(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Provider string `json:"provider"`
		BaseURL  string `json:"base_url"`
		APIKey   string `json:"api_key"`
		Model    string `json:"model"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	c := s.chatConfig(r.Context())
	p, err := s.formProvider(c, in.Provider, in.BaseURL, strings.TrimSpace(in.APIKey))
	if err != nil {
		return err
	}
	model := firstNonEmpty(strings.TrimSpace(in.Model), c.Model)
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	res := map[string]any{"ok": true, "label": "", "usage": 0, "limit": nil, "reply": ""}
	if p.OpenRouter() {
		if p.Key == "" {
			return badRequest("enter an OpenRouter API key first")
		}
		info, err := s.ai.Key(ctx, p)
		if err != nil {
			return badRequest("%v", err)
		}
		res["label"], res["usage"], res["limit"] = info.Label, info.Usage, info.Limit
	} else if model == "" {
		if _, err := s.ai.Models(ctx, p); err != nil {
			return badRequest("%v", err)
		}
	}
	if model != "" {
		reply, err := s.ai.Complete(ctx, p, model, []assistant.Message{{Role: "user", Content: "Reply with the single word OK."}}, 16)
		if err != nil {
			if p.OpenRouter() {
				return badRequest("the key works, but model %s failed: %v", model, err)
			}
			return badRequest("model %s failed: %v", model, err)
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
	out := make([]chatMessageOut, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, chatOut(m))
	}
	return ok(w, map[string]any{"conversation": conv, "messages": out})
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
	b.WriteString(`You are PortDock Assistant, built into PortDock — a free, open-source, self-hosted platform (like Heroku or Coolify) that deploys applications from Git repositories or Docker images, Docker Compose stacks, managed databases (PostgreSQL, MySQL, MariaDB, SQL Server, MongoDB, Redis, Valkey, ClickHouse) and one-click services on the organization's own servers, with domains and automatic HTTPS through a Traefik proxy, versions and rollbacks, metrics, alerts, backups, SSO and LDAP, deployment approvals and an audit log.
Help the user run PortDock and their applications: explain features, troubleshoot builds, deployments, containers, Docker, networking, DNS and TLS, and write configuration such as Dockerfiles, docker-compose.yml files and CI pipelines.
`)
	if c.Tools {
		b.WriteString(`You have tools that read THIS instance with the user's own permissions: projects, applications, deployments and their logs, the containers of every server with their logs, inspect data and resource usage, databases and their backups, services, servers, domains, jobs, versions, alerts, uptime monitors and the audit log, plus search_docs for PortDock's documentation and troubleshooting playbooks.
How to work:
- Use the tools instead of guessing. When the user names a container, application, database or server, find it (list_containers, list_applications…) and read the evidence before answering: container_logs (with errors_only or filter), container_inspect (exit code, OOMKilled, restarts, health check), container_stats, get_deployment for a failed deployment. Several tool calls in one reply are fine.
- Analyze, do not dump: group the errors, quote the few relevant lines, explain the probable cause, then the fix in PortDock terms (which tab or setting) with a snippet when useful. Say what you checked and what you could not (no permission, server unreachable, nothing found).
- For "how does PortDock…" questions call search_docs with English keywords and answer from it, naming the menu path. If the documentation does not say, say so instead of inventing.
- Secrets: never repeat environment variable values, passwords, tokens or connection strings, even when they appear in logs; if one was printed, say it should be rotated.
`)
		if c.Actions {
			b.WriteString(`Actions: you may propose deploy_application, rollback_deployment, restart or start of an application, container, database or service, backup_database and run_job. Calling them does not run them: the user sees a confirmation card and the action runs only after they press Confirm. Propose an action only when it clearly helps, say what it will do, and never claim it ran. Everything else — deleting or stopping anything, removing containers, images or volumes, changing variables, domains, settings, users or tokens, running commands on servers — you cannot do and must not try; explain where the user does it in the interface instead.
`)
		} else {
			b.WriteString(`You cannot change anything on the platform (the administrator turned the assistant's actions off): explain where the user does it in the interface.
`)
		}
	} else {
		b.WriteString(`You cannot see the user's servers or data unless they paste them here (the administrator turned the assistant's tools off). If you are not sure how PortDock behaves, say so instead of inventing menu names.
`)
	}
	b.WriteString(`Be concise and practical and use Markdown with fenced code blocks; keep tables small. Container names look like <application uuid>-<deployment id> (databases and services use their uuid); log timestamps from the tools are UTC.
`)
	fmt.Fprintf(&b, "You are talking to %s (role: %s) on PortDock %s. Answer in %s unless the user writes in another language.\n", firstNonEmpty(u.Name, u.Email), u.Role, Version, lang)
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
		// The list is newest first: the note about a reply's tools is added
		// before the reply here, so it follows it once reversed.
		if m.Role == "assistant" {
			if note := chatStepNote(parseChatMeta(m.Meta)); note != "" {
				out = append(out, assistant.Message{Role: "system", Content: note})
			}
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
// server-sent events: conversation, user, then delta (text), tool (a tool the
// assistant runs), action (an action waiting for the user's confirmation) and
// notice events in any order, then done (or error).
func (s *Server) sendChat(w http.ResponseWriter, r *http.Request) error {
	u, c, err := s.chatUser(r)
	if err != nil {
		return err
	}
	var in struct {
		ConversationID int64  `json:"conversation_id"`
		Content        string `json:"content"`
		// Context is what the browser shows: the page path, so "this
		// application" means something to the model.
		Context struct {
			Path string `json:"path"`
		} `json:"context"`
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
	if c.Tools {
		if page := s.chatPageContext(r, in.Context.Path); page != "" {
			msgs = append(msgs, assistant.Message{Role: "system", Content: "The user is currently looking at " + page + "."})
		}
	}

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

	// The model may ask for tools; their results go back to it until it
	// answers in text (bounded rounds and calls).
	var tools []assistant.Tool
	if c.Tools {
		tools = chatToolDefs(c.Actions)
	}
	onDelta := func(text string) error { return out.send("delta", map[string]string{"text": text}) }
	var (
		full   strings.Builder
		steps  []chatStep
		notice string
		calls  int
	)
	for round := 0; round <= chatToolRounds; round++ {
		text, toolCalls, serr := s.ai.StreamTools(streamCtx, c.provider(), c.Model, msgs, tools, onDelta)
		var unsupported *assistant.ToolsUnsupportedError
		if serr != nil && round == 0 && text == "" && errors.As(serr, &unsupported) {
			// This model refuses tools: answer without them, once.
			tools = nil
			notice = "This model does not support tools, so the assistant answered without reading the platform."
			_ = out.send("notice", map[string]string{"text": notice})
			text, toolCalls, serr = s.ai.StreamTools(streamCtx, c.provider(), c.Model, msgs, nil, onDelta)
		}
		if text != "" {
			if full.Len() > 0 {
				full.WriteString("\n\n")
			}
			full.WriteString(text)
		}
		err = serr
		if err != nil || len(toolCalls) == 0 {
			break
		}
		msgs = append(msgs, assistant.Message{Role: "assistant", Content: text, ToolCalls: toolCalls})
		if round+1 >= chatToolRounds || calls+len(toolCalls) > chatToolCalls {
			// Budget spent: the model answers with what it has.
			for _, tc := range toolCalls {
				msgs = append(msgs, assistant.Message{Role: "tool", ToolCallID: tc.ID, Content: `{"error":"the tool budget of this reply is spent: answer with what you have"}`})
			}
			tools = nil
			continue
		}
		for _, tc := range toolCalls {
			calls++
			result, step := s.chatRunTool(r, c, u, conv, tc, out)
			steps = append(steps, step)
			msgs = append(msgs, assistant.Message{Role: "tool", ToolCallID: tc.ID, Content: result})
		}
		if text != "" {
			_ = out.send("delta", map[string]string{"text": "\n\n"}) // the text before the tools and the answer after them
		}
	}
	reply := full.String()
	close(stopPing)
	<-pinged // nothing may write to the response once the handler returns
	cancel()
	aborted := err != nil && ctx.Err() != nil // the user pressed Stop or closed the page
	meta := &chatMeta{Steps: steps, Notice: notice}
	save := func(content string, failed bool) (chatMessageOut, error) {
		m := store.ChatMessage{ConversationID: conv.ID, Role: "assistant", Content: content, Model: c.Model, Error: failed, Meta: meta.encode(), CreatedAt: store.Now()}
		_, ierr := store.Insert(db, s.db, "chat_messages", &m)
		if ierr == nil {
			s.chatActs.attach(conv.ID, m.ID) // proposed actions now belong to this message
		}
		return chatOut(m), ierr
	}
	_, _ = s.db.ExecContext(db, "UPDATE chat_conversations SET updated_at = ? WHERE id = ?", store.Now(), conv.ID)
	if err != nil && !aborted {
		if errors.Is(err, context.DeadlineExceeded) {
			err = fmt.Errorf("the model did not finish within %s", chatReplyTimeout)
		}
		res := map[string]any{"error": err.Error()}
		if reply != "" || len(steps) > 0 {
			if m, ierr := save(reply, true); ierr == nil {
				res["message"] = m
			}
		}
		_ = out.send("error", res)
		return nil
	}
	if reply == "" && !aborted {
		res := map[string]any{"error": "the model returned an empty reply"}
		if len(steps) > 0 {
			if m, ierr := save("", true); ierr == nil {
				res["message"] = m
			}
		}
		_ = out.send("error", res)
		return nil
	}
	if reply == "" {
		return nil
	}
	m, err := save(reply, false)
	if err != nil {
		_ = out.send("error", map[string]any{"error": err.Error()})
		return nil
	}
	_ = out.send("done", m)
	return nil
}
