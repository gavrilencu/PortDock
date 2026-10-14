package deploy

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"github.com/portdock/portdock/internal/store"
)

// OTelKeys are the settings of the organization's OpenTelemetry collector.
var OTelKeys = []string{"otel_endpoint", "otel_protocol", "otel_headers"}

// otelEnv returns the OpenTelemetry SDK variables of a container of the
// application: the organization's OTLP endpoint (Settings → Observability)
// and the resource attributes that place it (project, environment, version).
// service names a Docker Compose service other than the application itself.
//
// Nothing is added when the container configures OpenTelemetry itself (its
// own endpoint must not receive the organization's headers) or disables it
// with OTEL_SDK_DISABLED; otherwise only the variables it does not set.
func (e *Engine) otelEnv(ctx context.Context, app store.Application, d *store.Deployment, service string, has func(string) bool) map[string]string {
	endpoint := e.db.Setting(ctx, "otel_endpoint")
	if endpoint == "" || has("OTEL_EXPORTER_OTLP_ENDPOINT") || has("OTEL_SDK_DISABLED") {
		return nil
	}
	protocol := e.db.Setting(ctx, "otel_protocol")
	if protocol == "" {
		protocol = "http/protobuf"
	}
	var envName, project string
	_ = e.db.QueryRowContext(ctx, "SELECT e.name, p.name FROM environments e JOIN projects p ON p.id = e.project_id WHERE e.id = ?",
		app.EnvironmentID).Scan(&envName, &project)
	attrs := []string{
		"service.namespace=" + baggageValue(project),
		"deployment.environment.name=" + baggageValue(envName),
		"deployment.environment=" + baggageValue(envName), // semantic conventions before 1.27
		"portdock.application=" + baggageValue(app.UUID),
	}
	if d != nil && d.CommitSHA != "" {
		attrs = append(attrs, "service.version="+baggageValue(short12(d.CommitSHA)))
	}
	if d != nil && d.PRNumber > 0 {
		attrs = append(attrs, "portdock.pull_request="+strconv.Itoa(d.PRNumber))
	}
	name := app.Name
	if service != "" {
		name += "-" + service
	}
	vars := map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT": endpoint,
		"OTEL_EXPORTER_OTLP_PROTOCOL": protocol,
		"OTEL_SERVICE_NAME":           name,
		"OTEL_RESOURCE_ATTRIBUTES":    strings.Join(attrs, ","),
	}
	if h := e.db.Setting(ctx, "otel_headers"); h != "" {
		if plain, err := e.box.Decrypt(h); err == nil && plain != "" {
			vars["OTEL_EXPORTER_OTLP_HEADERS"] = plain
		}
	}
	for k := range vars {
		if has(k) {
			delete(vars, k)
		}
	}
	return vars
}

// baggageValue percent-encodes a value of OTEL_RESOURCE_ATTRIBUTES (W3C
// baggage syntax).
func baggageValue(v string) string {
	return strings.ReplaceAll(url.QueryEscape(v), "+", "%20")
}
