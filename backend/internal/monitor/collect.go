// Package monitor samples server and container metrics every minute, stores
// them as time series (1-minute raw + hourly rollups) and evaluates alert rules.
package monitor

import (
	"regexp"
	"strconv"
	"strings"
)

// sampleScript gathers everything for one server in a single round trip.
// /proc/stat is read twice one second apart to compute CPU utilisation.
const sampleScript = `echo '#cpu'; head -1 /proc/stat; sleep 1; head -1 /proc/stat
echo '#mem'; grep -E '^(MemTotal|MemAvailable):' /proc/meminfo | awk '{print $2}'
echo '#disk'; df -Pk / | tail -1 | awk '{print $2, $3}'
echo '#load'; cat /proc/loadavg; nproc
echo '#net'; cat /proc/net/dev
echo '#ps'; docker ps -a --no-trunc --filter label=portdock.managed=true --format '{{.ID}}|{{.Label "portdock.kind"}}|{{.Label "portdock.resource"}}|{{.State}}' 2>/dev/null; docker ps -a --no-trunc --filter label=shipyard.managed=true --format '{{.ID}}|{{.Label "shipyard.kind"}}|{{.Label "shipyard.resource"}}|{{.State}}' 2>/dev/null
echo '#stats'; docker stats --no-stream --no-trunc --format '{{.ID}}|{{.CPUPerc}}|{{.MemUsage}}|{{.NetIO}}' 2>/dev/null
echo '#restarts'; ids=$({ docker ps -aq --no-trunc --filter label=portdock.managed=true; docker ps -aq --no-trunc --filter label=shipyard.managed=true; } 2>/dev/null | sort -u); [ -n "$ids" ] && docker inspect -f '{{.Id}} {{.RestartCount}}' $ids 2>/dev/null
echo '#end'`

// HostSample is a point-in-time reading of a server.
type HostSample struct {
	CPU                    float64 // percent of all cores
	MemUsed, MemTotal      float64
	DiskUsed, DiskTotal    float64
	Load1                  float64
	CPUs                   int
	NetRxBytes, NetTxBytes float64 // cumulative, all interfaces except loopback/docker bridges
}

// ContainerSample is one managed container.
type ContainerSample struct {
	ID       string
	Kind     string // application | database | service
	Resource string // resource UUID
	Running  bool
	CPU      float64 // percent, 100 = one core
	Mem      float64
	MemLimit float64
	NetRx    float64 // cumulative bytes
	NetTx    float64
	Restarts int
}

// Sample is a parsed script output.
type Sample struct {
	Host       HostSample
	Containers []ContainerSample
}

func sections(out string) map[string][]string {
	res := map[string][]string{}
	cur := ""
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "#") && !strings.Contains(line, " ") {
			cur = strings.TrimPrefix(line, "#")
			continue
		}
		if cur != "" && strings.TrimSpace(line) != "" {
			res[cur] = append(res[cur], line)
		}
	}
	return res
}

// cpuPercent computes utilisation between two "cpu ..." lines of /proc/stat.
func cpuPercent(a, b string) float64 {
	parse := func(l string) (idle, total float64) {
		f := strings.Fields(l)
		for i, v := range f[1:] {
			n, _ := strconv.ParseFloat(v, 64)
			total += n
			if i == 3 || i == 4 { // idle, iowait
				idle += n
			}
		}
		return
	}
	if !strings.HasPrefix(a, "cpu ") || !strings.HasPrefix(b, "cpu ") {
		return 0
	}
	i1, t1 := parse(a)
	i2, t2 := parse(b)
	if t2 <= t1 {
		return 0
	}
	return clamp((1-(i2-i1)/(t2-t1))*100, 0, 100)
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

var sizeRe = regexp.MustCompile(`^([0-9.]+)\s*([A-Za-z]*)$`)

// parseSize reads docker's human sizes: 12.5MiB, 1.2GB, 512kB, 0B.
func parseSize(s string) float64 {
	m := sizeRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0
	}
	n, _ := strconv.ParseFloat(m[1], 64)
	mult := map[string]float64{
		"": 1, "b": 1,
		"kb": 1e3, "mb": 1e6, "gb": 1e9, "tb": 1e12,
		"kib": 1 << 10, "mib": 1 << 20, "gib": 1 << 30, "tib": 1 << 40,
	}[strings.ToLower(m[2])]
	return n * mult
}

func splitPair(s string) (float64, float64) {
	a, b, _ := strings.Cut(s, "/")
	return parseSize(a), parseSize(b)
}

// Parse turns the output of sampleScript into a Sample.
func Parse(out string) Sample {
	sec := sections(out)
	var s Sample
	if c := sec["cpu"]; len(c) >= 2 {
		s.Host.CPU = cpuPercent(c[0], c[1])
	}
	if m := sec["mem"]; len(m) >= 2 {
		total, _ := strconv.ParseFloat(strings.TrimSpace(m[0]), 64)
		avail, _ := strconv.ParseFloat(strings.TrimSpace(m[1]), 64)
		s.Host.MemTotal, s.Host.MemUsed = total*1024, (total-avail)*1024
	}
	if d := sec["disk"]; len(d) >= 1 {
		if f := strings.Fields(d[0]); len(f) == 2 {
			t, _ := strconv.ParseFloat(f[0], 64)
			u, _ := strconv.ParseFloat(f[1], 64)
			s.Host.DiskTotal, s.Host.DiskUsed = t*1024, u*1024
		}
	}
	if l := sec["load"]; len(l) >= 1 {
		if f := strings.Fields(l[0]); len(f) > 0 {
			s.Host.Load1, _ = strconv.ParseFloat(f[0], 64)
		}
		if len(l) >= 2 {
			s.Host.CPUs, _ = strconv.Atoi(strings.TrimSpace(l[1]))
		}
	}
	for _, line := range sec["net"] {
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "lo" || strings.HasPrefix(name, "veth") || strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "br-") {
			continue
		}
		f := strings.Fields(rest)
		if len(f) >= 9 {
			rx, _ := strconv.ParseFloat(f[0], 64)
			tx, _ := strconv.ParseFloat(f[8], 64)
			s.Host.NetRxBytes += rx
			s.Host.NetTxBytes += tx
		}
	}

	byID := map[string]*ContainerSample{}
	var order []string
	for _, line := range sec["ps"] {
		p := strings.Split(line, "|")
		if len(p) < 4 || p[2] == "" {
			continue
		}
		if _, dup := byID[p[0]]; dup {
			continue
		}
		byID[p[0]] = &ContainerSample{ID: p[0], Kind: p[1], Resource: p[2], Running: p[3] == "running"}
		order = append(order, p[0])
	}
	for _, line := range sec["stats"] {
		p := strings.Split(line, "|")
		if len(p) < 4 {
			continue
		}
		c := byID[p[0]]
		if c == nil {
			continue
		}
		c.CPU, _ = strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(p[1]), "%"), 64)
		c.Mem, c.MemLimit = splitPair(p[2])
		c.NetRx, c.NetTx = splitPair(p[3])
	}
	for _, line := range sec["restarts"] {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		if c := byID[f[0]]; c != nil {
			c.Restarts, _ = strconv.Atoi(f[1])
		}
	}
	for _, id := range order {
		s.Containers = append(s.Containers, *byID[id])
	}
	return s
}
