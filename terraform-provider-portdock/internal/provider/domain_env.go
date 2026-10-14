package provider

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dsschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// ---- portdock_domain ----

type domainResource struct{ c *client }

type domainModel struct {
	ID            types.String `tfsdk:"id"`
	ApplicationID types.Int64  `tfsdk:"application_id"`
	Domain        types.String `tfsdk:"domain"`
	Port          types.Int64  `tfsdk:"port"`
	Host          types.String `tfsdk:"host"`
	HTTPS         types.Bool   `tfsdk:"https"`
}

type domainJSON struct {
	ID    int64  `json:"id"`
	Host  string `json:"host"`
	Path  string `json:"path"`
	Port  int64  `json:"port"`
	HTTPS bool   `json:"https"`
}

func newDomainResource() resource.Resource { return &domainResource{} }

func (r *domainResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_domain"
}

func (r *domainResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A domain routed to an application, with automatic HTTPS.",
		Attributes: map[string]schema.Attribute{
			"id":             idAttr(),
			"application_id": parentID("Application receiving the traffic."),
			"domain": schema.StringAttribute{Required: true, Description: "e.g. https://app.example.com or app.example.com/api",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"port":  optionalInt("Container port (0: the application's port)."),
			"host":  idAttr(),
			"https": schema.BoolAttribute{Computed: true},
		},
	}
}

func (r *domainResource) Configure(_ context.Context, req resource.ConfigureRequest, _ *resource.ConfigureResponse) {
	r.c = configured(req.ProviderData)
}

func hostPath(domain string) (string, string) {
	d := domain
	if !strings.Contains(d, "://") {
		d = "https://" + d
	}
	u, err := url.Parse(d)
	if err != nil {
		return strings.ToLower(domain), ""
	}
	p := strings.TrimRight(u.Path, "/")
	return strings.ToLower(u.Hostname()), p
}

func (r *domainResource) list(ctx context.Context, appID int64) ([]domainJSON, error) {
	var res struct {
		Domains []domainJSON `json:"domains"`
	}
	err := r.c.do(ctx, "GET", fmt.Sprintf("/api/applications/%d/domains", appID), nil, &res)
	return res.Domains, err
}

func (m *domainModel) from(d domainJSON) {
	m.ID, m.Host, m.HTTPS, m.Port = strID(d.ID), types.StringValue(d.Host+d.Path), types.BoolValue(d.HTTPS), types.Int64Value(d.Port)
}

func (r *domainResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan domainModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := map[string]any{"domain": plan.Domain.ValueString()}
	if !plan.Port.IsNull() && !plan.Port.IsUnknown() {
		body["port"] = plan.Port.ValueInt64()
	}
	var res struct {
		Domains []domainJSON `json:"domains"`
	}
	if err := r.c.do(ctx, "POST", fmt.Sprintf("/api/applications/%d/domains", plan.ApplicationID.ValueInt64()), body, &res); err != nil {
		resp.Diagnostics.AddError("Create domain", err.Error())
		return
	}
	host, p := hostPath(plan.Domain.ValueString())
	for _, d := range res.Domains {
		if d.Host == host && d.Path == p {
			plan.from(d)
			resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
			return
		}
	}
	resp.Diagnostics.AddError("Create domain", "the domain was added but is not listed")
}

func (r *domainResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state domainModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ds, err := r.list(ctx, state.ApplicationID.ValueInt64())
	if errors.Is(err, errNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Read domain", err.Error())
		return
	}
	for _, d := range ds {
		if d.ID == id64(state.ID) {
			state.from(d)
			resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *domainResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state domainModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := map[string]any{}
	if !plan.Port.IsNull() && !plan.Port.IsUnknown() {
		body["port"] = plan.Port.ValueInt64()
	}
	if err := r.c.do(ctx, "PATCH", fmt.Sprintf("/api/domains/%d", id64(state.ID)), body, nil); err != nil {
		resp.Diagnostics.AddError("Update domain", err.Error())
		return
	}
	plan.ID, plan.Host, plan.HTTPS = state.ID, state.Host, state.HTTPS
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *domainResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state domainModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if err := r.c.do(ctx, "DELETE", fmt.Sprintf("/api/domains/%d", id64(state.ID)), nil, nil); err != nil && !errors.Is(err, errNotFound) {
		resp.Diagnostics.AddError("Delete domain", err.Error())
	}
}

// ---- portdock_env_var ----

type envVarResource struct{ c *client }

type envVarModel struct {
	ID            types.String `tfsdk:"id"`
	ApplicationID types.Int64  `tfsdk:"application_id"`
	Key           types.String `tfsdk:"key"`
	Value         types.String `tfsdk:"value"`
	SecretRef     types.String `tfsdk:"secret_ref"`
	Secret        types.Bool   `tfsdk:"secret"`
}

type envVarJSON struct {
	ID         int64  `json:"id"`
	Key        string `json:"key"`
	Value      string `json:"value"`
	IsSecret   bool   `json:"is_secret"`
	RefStoreID int64  `json:"ref_store_id"`
	RefPath    string `json:"ref_path"`
	RefKey     string `json:"ref_key"`
}

func newEnvVarResource() resource.Resource { return &envVarResource{} }

var _ resource.ResourceWithValidateConfig = (*envVarResource)(nil)

// parseRef splits a secret store reference "store:path#key".
func parseRef(ref string) (store, path, key string, err error) {
	store, rest, found := strings.Cut(strings.TrimSpace(ref), ":")
	path, key, _ = strings.Cut(rest, "#")
	path = strings.Trim(path, "/")
	if !found || store == "" || path == "" {
		return "", "", "", fmt.Errorf("secret_ref %q: expected store:path#key, e.g. vault-prod:shop/db#password", ref)
	}
	return store, path, key, nil
}

// refBody is the API body of a reference: the store ID by its name.
func (r *envVarResource) refBody(ctx context.Context, ref string) (map[string]any, error) {
	store, path, key, err := parseRef(ref)
	if err != nil {
		return nil, err
	}
	var stores []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err := r.c.do(ctx, "GET", "/api/secret-stores/names", nil, &stores); err != nil {
		return nil, err
	}
	for _, s := range stores {
		if s.Name == store {
			return map[string]any{"ref_store_id": s.ID, "ref_path": path, "ref_key": key}, nil
		}
	}
	return nil, fmt.Errorf("no secret store named %s (Settings → Secret managers)", store)
}

// refOf renders the reference of a variable read back from the API.
func (r *envVarResource) refOf(ctx context.Context, v envVarJSON) string {
	var stores []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	_ = r.c.do(ctx, "GET", "/api/secret-stores/names", nil, &stores)
	name := ""
	for _, s := range stores {
		if s.ID == v.RefStoreID {
			name = s.Name
		}
	}
	if v.RefKey == "" {
		return name + ":" + v.RefPath
	}
	return name + ":" + v.RefPath + "#" + v.RefKey
}

func (r *envVarResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m envVarModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || m.Value.IsUnknown() || m.SecretRef.IsUnknown() {
		return
	}
	if m.Value.IsNull() == m.SecretRef.IsNull() {
		resp.Diagnostics.AddError("Invalid variable", "set exactly one of value and secret_ref")
		return
	}
	if !m.SecretRef.IsNull() {
		if _, _, _, err := parseRef(m.SecretRef.ValueString()); err != nil {
			resp.Diagnostics.AddError("Invalid secret_ref", err.Error())
		}
	}
}

func (r *envVarResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_env_var"
}

func (r *envVarResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A runtime variable of an application (encrypted at rest), or one read from a secret manager when a deployment starts. Redeploy the application to apply changes.",
		Attributes: map[string]schema.Attribute{
			"id":             idAttr(),
			"application_id": parentID("Application of the variable."),
			"key":            schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"value":          schema.StringAttribute{Optional: true, Sensitive: true, Description: "The value. Set this or secret_ref."},
			"secret_ref": schema.StringAttribute{Optional: true,
				Description: "Read the value from a secret manager of Settings → Secret managers: store:path#key, e.g. vault-prod:shop/db#password. PortDock never stores the value."},
			"secret": schema.BoolAttribute{Optional: true, Computed: true, Description: "Hidden in the interface and in exports."},
		},
	}
}

func (r *envVarResource) Configure(_ context.Context, req resource.ConfigureRequest, _ *resource.ConfigureResponse) {
	r.c = configured(req.ProviderData)
}

func (r *envVarResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan envVarModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	secret := !plan.Secret.IsNull() && !plan.Secret.IsUnknown() && plan.Secret.ValueBool()
	body := map[string]any{"key": plan.Key.ValueString(), "value": plan.Value.ValueString(), "is_secret": secret}
	if !plan.SecretRef.IsNull() {
		ref, err := r.refBody(ctx, plan.SecretRef.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Create variable", err.Error())
			return
		}
		ref["key"] = plan.Key.ValueString()
		body = ref
	}
	var v envVarJSON
	if err := r.c.do(ctx, "POST", fmt.Sprintf("/api/applications/%d/envs", plan.ApplicationID.ValueInt64()), body, &v); err != nil {
		resp.Diagnostics.AddError("Create variable", err.Error())
		return
	}
	plan.ID, plan.Secret = strID(v.ID), types.BoolValue(v.IsSecret)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *envVarResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state envVarModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var vs []envVarJSON
	err := r.c.do(ctx, "GET", fmt.Sprintf("/api/applications/%d/envs", state.ApplicationID.ValueInt64()), nil, &vs)
	if errors.Is(err, errNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Read variable", err.Error())
		return
	}
	for _, v := range vs {
		if v.ID == id64(state.ID) {
			state.Key, state.Secret = types.StringValue(v.Key), types.BoolValue(v.IsSecret)
			if v.RefStoreID > 0 {
				state.Value, state.SecretRef = types.StringNull(), types.StringValue(r.refOf(ctx, v))
			} else {
				state.Value, state.SecretRef = types.StringValue(v.Value), types.StringNull()
			}
			resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *envVarResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state envVarModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := map[string]any{"value": plan.Value.ValueString()}
	if !state.SecretRef.IsNull() {
		body["ref_store_id"] = 0 // from a secret manager back to a value
	}
	if !plan.Secret.IsNull() && !plan.Secret.IsUnknown() {
		body["is_secret"] = plan.Secret.ValueBool()
	}
	if !plan.SecretRef.IsNull() {
		ref, err := r.refBody(ctx, plan.SecretRef.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Update variable", err.Error())
			return
		}
		body = ref
	}
	if err := r.c.do(ctx, "PATCH", fmt.Sprintf("/api/envs/%d", id64(state.ID)), body, nil); err != nil {
		resp.Diagnostics.AddError("Update variable", err.Error())
		return
	}
	plan.ID = state.ID
	if !plan.SecretRef.IsNull() {
		plan.Secret = types.BoolValue(true)
	} else if plan.Secret.IsUnknown() {
		plan.Secret = state.Secret
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *envVarResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state envVarModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if err := r.c.do(ctx, "DELETE", fmt.Sprintf("/api/envs/%d", id64(state.ID)), nil, nil); err != nil && !errors.Is(err, errNotFound) {
		resp.Diagnostics.AddError("Delete variable", err.Error())
	}
}

// ---- data "portdock_server" ----

type serverDataSource struct{ c *client }

type serverModel struct {
	Name types.String `tfsdk:"name"`
	ID   types.Int64  `tfsdk:"id"`
	Host types.String `tfsdk:"host"`
}

func newServerDataSource() datasource.DataSource { return &serverDataSource{} }

func (d *serverDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server"
}

func (d *serverDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dsschema.Schema{
		Description: "A server of PortDock, by name.",
		Attributes: map[string]dsschema.Attribute{
			"name": dsschema.StringAttribute{Required: true},
			"id":   dsschema.Int64Attribute{Computed: true},
			"host": dsschema.StringAttribute{Computed: true},
		},
	}
}

func (d *serverDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, _ *datasource.ConfigureResponse) {
	d.c = configured(req.ProviderData)
}

func (d *serverDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m serverModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var servers []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
		Host string `json:"host"`
	}
	if err := d.c.do(ctx, "GET", "/api/servers", nil, &servers); err != nil {
		resp.Diagnostics.AddError("List servers", err.Error())
		return
	}
	for _, s := range servers {
		if s.Name == m.Name.ValueString() {
			m.ID, m.Host = types.Int64Value(s.ID), types.StringValue(s.Host)
			resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
			return
		}
	}
	resp.Diagnostics.AddError("Server not found", fmt.Sprintf("no server named %q", m.Name.ValueString()))
}
