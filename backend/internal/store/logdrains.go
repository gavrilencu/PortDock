package store

import (
	"strconv"
	"strings"
)

// LogDrain is a destination of the logs of every container PortDock runs.
type LogDrain struct {
	ID   int64  `db:"id" json:"id"`
	Name string `db:"name" json:"name"`
	// Type is loki, elasticsearch, gelf, splunk, syslog or http.
	Type string `db:"type" json:"type"`
	URL  string `db:"url" json:"url"`
	// Config is the encrypted JSON of logdrain.Config (credentials, options).
	Config       string `db:"config" json:"-"`
	ProjectIDs   string `db:"project_ids" json:"-"`
	IncludeProxy bool   `db:"include_proxy" json:"include_proxy"`
	Enabled      bool   `db:"enabled" json:"enabled"`
	CreatedAt    string `db:"created_at" json:"created_at"`
}

// Projects returns the projects the drain is limited to (none: every project).
func (d LogDrain) Projects() []int64 {
	var ids []int64
	for _, s := range strings.Split(d.ProjectIDs, ",") {
		if id, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}
