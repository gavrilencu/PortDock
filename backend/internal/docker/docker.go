// Package docker wraps the Docker CLI on top of an executor, so the same code
// manages local and remote (SSH) Docker engines.
package docker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/shipyard/shipyard/internal/executor"
)

var q = executor.Quote

// Labels used on every workload Shipyard manages.
const (
	LabelManaged    = "shipyard.managed"
	LabelKind       = "shipyard.kind" // application | database | service | proxy
	LabelResource   = "shipyard.resource"
	LabelDeployment = "shipyard.deployment"
	LabelPR         = "shipyard.pr"
)

type Container struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Image     string            `json:"image"`
	State     string            `json:"state"`
	Status    string            `json:"status"`
	Ports     string            `json:"ports"`
	Networks  string            `json:"networks"`
	CreatedAt string            `json:"created_at"`
	Labels    map[string]string `json:"labels"`
}

type rawContainer struct {
	ID        string `json:"ID"`
	Names     string `json:"Names"`
	Image     string `json:"Image"`
	State     string `json:"State"`
	Status    string `json:"Status"`
	Ports     string `json:"Ports"`
	Networks  string `json:"Networks"`
	CreatedAt string `json:"CreatedAt"`
	Labels    string `json:"Labels"`
}

func parseLabels(s string) map[string]string {
	m := map[string]string{}
	for _, kv := range strings.Split(s, ",") {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
}

func jsonLines[T any](out string) ([]T, error) {
	res := []T{}
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] != '{' {
			continue
		}
		var v T
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			return nil, err
		}
		res = append(res, v)
	}
	return res, sc.Err()
}

func Version(ctx context.Context, e executor.Executor) (string, error) {
	return executor.Output(ctx, e, "docker version --format '{{.Server.Version}}'")
}

// PS lists containers. Filters are passed as docker `--filter` values, e.g. "label=shipyard.resource=abc".
func PS(ctx context.Context, e executor.Executor, all bool, filters ...string) ([]Container, error) {
	cmd := "docker ps --no-trunc --format '{{json .}}'"
	if all {
		cmd += " -a"
	}
	for _, f := range filters {
		cmd += " --filter " + q(f)
	}
	out, err := executor.Output(ctx, e, cmd)
	if err != nil {
		return nil, err
	}
	raws, err := jsonLines[rawContainer](out)
	if err != nil {
		return nil, err
	}
	res := make([]Container, 0, len(raws))
	for _, r := range raws {
		res = append(res, Container{
			ID: r.ID, Name: r.Names, Image: r.Image, State: r.State, Status: r.Status,
			Ports: r.Ports, Networks: r.Networks, CreatedAt: r.CreatedAt, Labels: parseLabels(r.Labels),
		})
	}
	return res, nil
}

// ContainerAction runs start/stop/restart/pause/unpause/kill/rm on a container.
func ContainerAction(ctx context.Context, e executor.Executor, action, id string) error {
	var cmd string
	switch action {
	case "start", "stop", "restart", "pause", "unpause", "kill":
		cmd = "docker " + action + " " + q(id)
	case "remove", "rm":
		cmd = "docker rm -f " + q(id)
	default:
		return fmt.Errorf("unknown action %q", action)
	}
	_, err := executor.Output(ctx, e, cmd)
	return err
}

func Inspect(ctx context.Context, e executor.Executor, id string) (json.RawMessage, error) {
	out, err := executor.Output(ctx, e, "docker inspect "+q(id))
	if err != nil {
		return nil, err
	}
	return json.RawMessage(out), nil
}

// State returns the container's state (running, exited, ...) and health ("" when no healthcheck).
func State(ctx context.Context, e executor.Executor, name string) (state, health string, err error) {
	out, err := executor.Output(ctx, e, "docker inspect --format '{{.State.Status}}|{{if .State.Health}}{{.State.Health.Status}}{{end}}' "+q(name))
	if err != nil {
		return "", "", err
	}
	state, health, _ = strings.Cut(out, "|")
	return state, health, nil
}

// Logs streams container logs to w until ctx is cancelled (when follow is true).
func Logs(ctx context.Context, e executor.Executor, id string, tail int, follow, timestamps bool, w io.Writer) error {
	cmd := "docker logs --tail " + strconv.Itoa(tail)
	if follow {
		cmd += " -f"
	}
	if timestamps {
		cmd += " -t"
	}
	cmd += " " + q(id)
	return e.Run(ctx, cmd, executor.RunOpts{Stdout: w, Stderr: w})
}

type Stat struct {
	Container string `json:"container"`
	Name      string `json:"name"`
	CPUPerc   string `json:"cpu"`
	MemUsage  string `json:"mem_usage"`
	MemPerc   string `json:"mem"`
	NetIO     string `json:"net_io"`
	BlockIO   string `json:"block_io"`
	PIDs      string `json:"pids"`
}

func Stats(ctx context.Context, e executor.Executor, ids ...string) ([]Stat, error) {
	cmd := "docker stats --no-stream --format '{{json .}}'"
	for _, id := range ids {
		cmd += " " + q(id)
	}
	out, err := executor.Output(ctx, e, cmd)
	if err != nil {
		return nil, err
	}
	type raw struct {
		Container, Name, CPUPerc, MemUsage, MemPerc, NetIO, BlockIO, PIDs string
	}
	raws, err := jsonLines[raw](out)
	if err != nil {
		return nil, err
	}
	res := make([]Stat, 0, len(raws))
	for _, r := range raws {
		res = append(res, Stat{r.Container, r.Name, r.CPUPerc, r.MemUsage, r.MemPerc, r.NetIO, r.BlockIO, r.PIDs})
	}
	return res, nil
}

type Image struct {
	ID         string `json:"id"`
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	Size       string `json:"size"`
	CreatedAt  string `json:"created_at"`
}

func Images(ctx context.Context, e executor.Executor) ([]Image, error) {
	out, err := executor.Output(ctx, e, "docker images --format '{{json .}}'")
	if err != nil {
		return nil, err
	}
	type raw struct{ ID, Repository, Tag, Size, CreatedAt string }
	raws, err := jsonLines[raw](out)
	if err != nil {
		return nil, err
	}
	res := make([]Image, 0, len(raws))
	for _, r := range raws {
		res = append(res, Image{r.ID, r.Repository, r.Tag, r.Size, r.CreatedAt})
	}
	return res, nil
}

type Volume struct {
	Name   string `json:"name"`
	Driver string `json:"driver"`
	Labels string `json:"labels"`
}

func Volumes(ctx context.Context, e executor.Executor) ([]Volume, error) {
	out, err := executor.Output(ctx, e, "docker volume ls --format '{{json .}}'")
	if err != nil {
		return nil, err
	}
	type raw struct{ Name, Driver, Labels string }
	raws, err := jsonLines[raw](out)
	if err != nil {
		return nil, err
	}
	res := make([]Volume, 0, len(raws))
	for _, r := range raws {
		res = append(res, Volume{r.Name, r.Driver, r.Labels})
	}
	return res, nil
}

type Network struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Driver string `json:"driver"`
	Scope  string `json:"scope"`
}

func Networks(ctx context.Context, e executor.Executor) ([]Network, error) {
	out, err := executor.Output(ctx, e, "docker network ls --format '{{json .}}'")
	if err != nil {
		return nil, err
	}
	type raw struct{ ID, Name, Driver, Scope string }
	raws, err := jsonLines[raw](out)
	if err != nil {
		return nil, err
	}
	res := make([]Network, 0, len(raws))
	for _, r := range raws {
		res = append(res, Network{r.ID, r.Name, r.Driver, r.Scope})
	}
	return res, nil
}

func RemoveImage(ctx context.Context, e executor.Executor, id string) error {
	_, err := executor.Output(ctx, e, "docker rmi -f "+q(id))
	return err
}

func RemoveVolume(ctx context.Context, e executor.Executor, name string) error {
	_, err := executor.Output(ctx, e, "docker volume rm -f "+q(name))
	return err
}

// Prune removes unused data. Scope: containers | images | volumes | builder | networks | all.
func Prune(ctx context.Context, e executor.Executor, scope string) (string, error) {
	var cmd string
	switch scope {
	case "containers":
		cmd = "docker container prune -f"
	case "images":
		cmd = "docker image prune -af"
	case "volumes":
		cmd = "docker volume prune -f"
	case "builder":
		cmd = "docker builder prune -af"
	case "networks":
		cmd = "docker network prune -f"
	case "all":
		cmd = "docker system prune -af"
	default:
		return "", fmt.Errorf("unknown prune scope %q", scope)
	}
	return executor.Output(ctx, e, cmd)
}

// EnsureNetwork creates an attachable bridge network if it does not exist.
func EnsureNetwork(ctx context.Context, e executor.Executor, name string) error {
	_, err := executor.Output(ctx, e, fmt.Sprintf("docker network inspect %s >/dev/null 2>&1 || docker network create --attachable %s", q(name), q(name)))
	return err
}

type Metrics struct {
	CPUs        int     `json:"cpus"`
	Load1       float64 `json:"load1"`
	Load5       float64 `json:"load5"`
	Load15      float64 `json:"load15"`
	MemTotal    int64   `json:"mem_total"`
	MemUsed     int64   `json:"mem_used"`
	DiskTotal   int64   `json:"disk_total"`
	DiskUsed    int64   `json:"disk_used"`
	Uptime      float64 `json:"uptime"`
	OS          string  `json:"os"`
	Kernel      string  `json:"kernel"`
	Containers  int     `json:"containers"`
	RunningCont int     `json:"running_containers"`
}

// ServerMetrics gathers host-level metrics with standard Linux tools.
func ServerMetrics(ctx context.Context, e executor.Executor) (Metrics, error) {
	script := `nproc; cat /proc/loadavg; grep -E '^(MemTotal|MemAvailable):' /proc/meminfo | awk '{print $2}'; ` +
		`df -Pk / | tail -1 | awk '{print $2, $3}'; cut -d' ' -f1 /proc/uptime; ` +
		`(. /etc/os-release 2>/dev/null && echo "$PRETTY_NAME") || echo unknown; uname -r; ` +
		`docker ps -aq 2>/dev/null | wc -l; docker ps -q 2>/dev/null | wc -l`
	out, err := executor.Output(ctx, e, script)
	var m Metrics
	if err != nil {
		return m, err
	}
	lines := strings.Split(out, "\n")
	get := func(i int) string {
		if i < len(lines) {
			return strings.TrimSpace(lines[i])
		}
		return ""
	}
	m.CPUs, _ = strconv.Atoi(get(0))
	if f := strings.Fields(get(1)); len(f) >= 3 {
		m.Load1, _ = strconv.ParseFloat(f[0], 64)
		m.Load5, _ = strconv.ParseFloat(f[1], 64)
		m.Load15, _ = strconv.ParseFloat(f[2], 64)
	}
	total, _ := strconv.ParseInt(get(2), 10, 64)
	avail, _ := strconv.ParseInt(get(3), 10, 64)
	m.MemTotal, m.MemUsed = total*1024, (total-avail)*1024
	if f := strings.Fields(get(4)); len(f) == 2 {
		dt, _ := strconv.ParseInt(f[0], 10, 64)
		du, _ := strconv.ParseInt(f[1], 10, 64)
		m.DiskTotal, m.DiskUsed = dt*1024, du*1024
	}
	m.Uptime, _ = strconv.ParseFloat(get(5), 64)
	m.OS = get(6)
	m.Kernel = get(7)
	m.Containers, _ = strconv.Atoi(get(8))
	m.RunningCont, _ = strconv.Atoi(get(9))
	return m, nil
}
