package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// mcpBridge serves the Model Context Protocol on standard input and output
// for AI clients that start a local command, and posts each message to the
// PortDock MCP endpoint with the saved token. Messages are handled at once,
// so a long deploy_application with wait does not hold up the others.
func (c *client) mcpBridge(in io.Reader, out io.Writer) error {
	hc := &http.Client{Timeout: 15 * time.Minute}
	var mu sync.Mutex
	write := func(b []byte) {
		// One message per line: compact whatever the server sent.
		var buf bytes.Buffer
		if json.Compact(&buf, b) != nil {
			buf.Reset()
			buf.Write(bytes.TrimSpace(b))
		}
		buf.WriteByte('\n')
		mu.Lock()
		defer mu.Unlock()
		out.Write(buf.Bytes())
	}
	var wg sync.WaitGroup
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		msg := append([]byte(nil), line...)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if b := c.mcpPost(hc, msg); b != nil {
				write(b)
			}
		}()
	}
	wg.Wait()
	return sc.Err()
}

// mcpPost forwards one message and returns the answer to write, or nil for
// a notification. A failed request becomes a JSON-RPC error for the client.
func (c *client) mcpPost(hc *http.Client, msg []byte) []byte {
	req, err := http.NewRequest("POST", c.url+"/api/mcp", bytes.NewReader(msg))
	if err != nil {
		return rpcFailure(msg, err.Error())
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("User-Agent", "pdctl/"+version)
	resp, err := hc.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pdctl mcp:", err)
		return rpcFailure(msg, "PortDock is unreachable: "+err.Error())
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	switch {
	case resp.StatusCode == http.StatusAccepted:
		return nil
	case resp.StatusCode == http.StatusOK:
		return b
	}
	e := apiError(resp.StatusCode, b).Error()
	fmt.Fprintln(os.Stderr, "pdctl mcp:", e)
	return rpcFailure(msg, e)
}

// rpcFailure answers each request of msg (one message or a batch) with an
// error; notifications get no answer.
func rpcFailure(msg []byte, text string) []byte {
	type req struct {
		ID json.RawMessage `json:"id"`
	}
	var reqs []req
	if strings.HasPrefix(string(msg), "[") {
		_ = json.Unmarshal(msg, &reqs)
	} else {
		var one req
		if json.Unmarshal(msg, &one) != nil {
			one.ID = json.RawMessage("null")
		}
		reqs = []req{one}
	}
	var out []any
	for _, r := range reqs {
		if len(r.ID) == 0 {
			continue
		}
		out = append(out, map[string]any{"jsonrpc": "2.0", "id": r.ID, "error": map[string]any{"code": -32603, "message": text}})
	}
	switch len(out) {
	case 0:
		return nil
	case 1:
		if !strings.HasPrefix(string(msg), "[") {
			b, _ := json.Marshal(out[0])
			return b
		}
	}
	b, _ := json.Marshal(out)
	return b
}
