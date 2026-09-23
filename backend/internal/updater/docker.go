package updater

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Docker is a minimal Docker Engine API client over the unix socket.
type Docker struct {
	c *http.Client
}

// SocketPath is where the engine socket is mounted inside the Shipyard container.
var SocketPath = "/var/run/docker.sock"

func NewDocker() *Docker {
	return &Docker{c: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", SocketPath)
		},
	}}}
}

// Available reports whether the engine socket is reachable.
func (d *Docker) Available(ctx context.Context) bool {
	if _, err := os.Stat(SocketPath); err != nil {
		return false
	}
	res, err := d.do(ctx, http.MethodGet, "/_ping", nil, nil)
	if err != nil {
		return false
	}
	res.Body.Close()
	return res.StatusCode == 200
}

func (d *Docker) do(ctx context.Context, method, path string, body any, headers map[string]string) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+path, r)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return d.c.Do(req)
}

// call performs a request and decodes a JSON response into out (may be nil).
func (d *Docker) call(ctx context.Context, method, path string, body, out any) error {
	res, err := d.do(ctx, method, path, body, nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if res.StatusCode >= 300 && res.StatusCode != http.StatusNotModified {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Message == "" {
			e.Message = strings.TrimSpace(string(raw))
		}
		return &APIError{Status: res.StatusCode, Message: e.Message}
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("docker API %d: %s", e.Status, e.Message) }

func IsNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == 404
}

// Container is the subset of an inspect result the updater needs; raw keeps
// every field so the configuration can be copied verbatim.
type Container struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	Image  string `json:"Image"` // image ID
	Config struct {
		Image  string            `json:"Image"`
		Env    []string          `json:"Env"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		Status   string `json:"Status"`
		Running  bool   `json:"Running"`
		ExitCode int    `json:"ExitCode"`
		Health   *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
	Mounts []struct {
		Type        string `json:"Type"`
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
	} `json:"Mounts"`
	Raw map[string]json.RawMessage `json:"-"`
}

func (d *Docker) Inspect(ctx context.Context, id string) (Container, error) {
	var raw map[string]json.RawMessage
	if err := d.call(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/json", nil, &raw); err != nil {
		return Container{}, err
	}
	b, _ := json.Marshal(raw)
	var c Container
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	c.Raw = raw
	c.Name = strings.TrimPrefix(c.Name, "/")
	return c, nil
}

// ImageConfig returns the Config section of an image (defaults baked into it).
func (d *Docker) ImageConfig(ctx context.Context, ref string) (map[string]json.RawMessage, error) {
	var img struct {
		Config map[string]json.RawMessage `json:"Config"`
	}
	if err := d.call(ctx, http.MethodGet, "/images/"+url.PathEscape(ref)+"/json", nil, &img); err != nil {
		return nil, err
	}
	return img.Config, nil
}

func (d *Docker) ImageExists(ctx context.Context, ref string) bool {
	_, err := d.ImageConfig(ctx, ref)
	return err == nil
}

// Pull downloads ref, reporting progress lines to log. auth is an optional registry token.
func (d *Docker) Pull(ctx context.Context, ref, user, token string, log func(string)) error {
	image, tag := ref, "latest"
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		image, tag = ref[:i], ref[i+1:]
	}
	headers := map[string]string{}
	if token != "" {
		if user == "" {
			user = "token"
		}
		a, _ := json.Marshal(map[string]string{"username": user, "password": token})
		headers["X-Registry-Auth"] = base64.URLEncoding.EncodeToString(a)
	}
	res, err := d.do(ctx, http.MethodPost, "/images/create?fromImage="+url.QueryEscape(image)+"&tag="+url.QueryEscape(tag), nil, headers)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return &APIError{Status: res.StatusCode, Message: strings.TrimSpace(string(b))}
	}
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	last := ""
	for sc.Scan() {
		var m struct {
			Status   string `json:"status"`
			Error    string `json:"error"`
			Progress string `json:"progress"`
			ID       string `json:"id"`
		}
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		if m.Error != "" {
			return errors.New(m.Error)
		}
		// Only log state changes, not every progress tick.
		line := strings.TrimSpace(m.ID + " " + m.Status)
		if m.Progress == "" && line != last && log != nil {
			log(line)
			last = line
		}
	}
	return sc.Err()
}

func (d *Docker) Stop(ctx context.Context, id string, timeout time.Duration) error {
	err := d.call(ctx, http.MethodPost, fmt.Sprintf("/containers/%s/stop?t=%d", url.PathEscape(id), int(timeout.Seconds())), nil, nil)
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == 304 {
		return nil // already stopped
	}
	return err
}

func (d *Docker) Start(ctx context.Context, id string) error {
	err := d.call(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/start", nil, nil)
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == 304 {
		return nil
	}
	return err
}

func (d *Docker) Rename(ctx context.Context, id, name string) error {
	return d.call(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/rename?name="+url.QueryEscape(name), nil, nil)
}

func (d *Docker) Remove(ctx context.Context, id string) error {
	return d.call(ctx, http.MethodDelete, "/containers/"+url.PathEscape(id)+"?force=1&v=0", nil, nil)
}

// Create creates a container from a full create body and returns its ID.
func (d *Docker) Create(ctx context.Context, name string, body map[string]any) (string, error) {
	var out struct {
		ID string `json:"Id"`
	}
	if err := d.call(ctx, http.MethodPost, "/containers/create?name="+url.QueryEscape(name), body, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// Logs returns the last lines of a container's output (demultiplexed).
func (d *Docker) Logs(ctx context.Context, id string, tail int) (string, error) {
	res, err := d.do(ctx, http.MethodGet, fmt.Sprintf("/containers/%s/logs?stdout=1&stderr=1&tail=%d", url.PathEscape(id), tail), nil, nil)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	// Non-TTY logs are framed: 8-byte header (stream, 0, 0, 0, size uint32 BE) + payload.
	var out bytes.Buffer
	for len(raw) >= 8 && (raw[0] == 1 || raw[0] == 2) {
		n := int(raw[4])<<24 | int(raw[5])<<16 | int(raw[6])<<8 | int(raw[7])
		if 8+n > len(raw) {
			break
		}
		out.Write(raw[8 : 8+n])
		raw = raw[8+n:]
	}
	if out.Len() == 0 {
		out.Write(raw)
	}
	return out.String(), nil
}
