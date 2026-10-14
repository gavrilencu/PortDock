package provider

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func idAttr() schema.StringAttribute {
	return schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}}
}

func optionalString(desc string) schema.StringAttribute {
	return schema.StringAttribute{Optional: true, Computed: true, Description: desc, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}}
}

func parentID(desc string) schema.Int64Attribute {
	return schema.Int64Attribute{Required: true, Description: desc, PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()}}
}

func id64(s types.String) int64 {
	n, _ := strconv.ParseInt(s.ValueString(), 10, 64)
	return n
}

func strID(n int64) types.String { return types.StringValue(strconv.FormatInt(n, 10)) }

// ---- portdock_project ----

type projectResource struct{ c *client }

type projectModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	DefaultRole types.String `tfsdk:"default_role"`
	Production  types.Int64  `tfsdk:"production_environment_id"`
}

type projectJSON struct {
	ID           int64             `json:"id"`
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	DefaultRole  string            `json:"default_role"`
	Environments []environmentJSON `json:"environments"`
}

func newProjectResource() resource.Resource { return &projectResource{} }

func (r *projectResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project"
}

func (r *projectResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A project: environments with their applications, databases and services.",
		Attributes: map[string]schema.Attribute{
			"id":           idAttr(),
			"name":         schema.StringAttribute{Required: true},
			"description":  optionalString("Free text."),
			"default_role": optionalString("Role of members without an explicit membership: none, viewer, developer or maintainer."),
			"production_environment_id": schema.Int64Attribute{Computed: true, Description: "The production environment every project starts with.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
		},
	}
}

func (r *projectResource) Configure(_ context.Context, req resource.ConfigureRequest, _ *resource.ConfigureResponse) {
	r.c = configured(req.ProviderData)
}

func (r *projectResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (m *projectModel) from(p projectJSON) {
	m.ID, m.Name, m.Description, m.DefaultRole = strID(p.ID), types.StringValue(p.Name), types.StringValue(p.Description), types.StringValue(p.DefaultRole)
	if p.Environments == nil { // an update answers without the environments
		return
	}
	m.Production = types.Int64Null()
	for _, e := range p.Environments {
		if e.Name == "production" {
			m.Production = types.Int64Value(e.ID)
		}
	}
}

func (m projectModel) body() map[string]any {
	b := map[string]any{"name": m.Name.ValueString()}
	if !m.Description.IsUnknown() && !m.Description.IsNull() {
		b["description"] = m.Description.ValueString()
	}
	if !m.DefaultRole.IsUnknown() && !m.DefaultRole.IsNull() {
		b["default_role"] = m.DefaultRole.ValueString()
	}
	return b
}

func (r *projectResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan projectModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var p projectJSON
	if err := r.c.do(ctx, "POST", "/api/projects", plan.body(), &p); err != nil {
		resp.Diagnostics.AddError("Create project", err.Error())
		return
	}
	plan.from(p)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// mergeName completes a partial update with the current values the API expects.
func mergeName(body map[string]any, p projectJSON) map[string]any {
	if _, ok := body["description"]; !ok {
		body["description"] = p.Description
	}
	if _, ok := body["default_role"]; !ok {
		body["default_role"] = p.DefaultRole
	}
	return body
}

func (r *projectResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state projectModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var p projectJSON
	if err := r.c.do(ctx, "GET", fmt.Sprintf("/api/projects/%d", id64(state.ID)), nil, &p); err != nil {
		if errors.Is(err, errNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read project", err.Error())
		return
	}
	state.from(p)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *projectResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state projectModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var p projectJSON
	if err := r.c.do(ctx, "PATCH", fmt.Sprintf("/api/projects/%d", id64(state.ID)), mergeName(plan.body(), projectJSON{Description: state.Description.ValueString(), DefaultRole: state.DefaultRole.ValueString()}), &p); err != nil {
		resp.Diagnostics.AddError("Update project", err.Error())
		return
	}
	plan.Production = state.Production
	plan.from(p)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *projectResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state projectModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if err := r.c.do(ctx, "DELETE", fmt.Sprintf("/api/projects/%d", id64(state.ID)), nil, nil); err != nil && !errors.Is(err, errNotFound) {
		resp.Diagnostics.AddError("Delete project", err.Error())
	}
}

// ---- portdock_environment ----

type environmentResource struct{ c *client }

type environmentModel struct {
	ID              types.String `tfsdk:"id"`
	ProjectID       types.Int64  `tfsdk:"project_id"`
	Name            types.String `tfsdk:"name"`
	Protected       types.Bool   `tfsdk:"protected"`
	RequireApproval types.Bool   `tfsdk:"require_approval"`
}

type environmentJSON struct {
	ID              int64  `json:"id"`
	ProjectID       int64  `json:"project_id"`
	Name            string `json:"name"`
	Protected       bool   `json:"protected"`
	RequireApproval bool   `json:"require_approval"`
}

func newEnvironmentResource() resource.Resource { return &environmentResource{} }

func (r *environmentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_environment"
}

func (r *environmentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "An environment of a project (production, staging…).",
		Attributes: map[string]schema.Attribute{
			"id":               idAttr(),
			"project_id":       parentID("Project of the environment."),
			"name":             schema.StringAttribute{Required: true},
			"protected":        schema.BoolAttribute{Optional: true, Computed: true, Description: "Only maintainers change and deploy without approval."},
			"require_approval": schema.BoolAttribute{Optional: true, Computed: true, Description: "Every deployment needs a maintainer's approval."},
		},
	}
}

func (r *environmentResource) Configure(_ context.Context, req resource.ConfigureRequest, _ *resource.ConfigureResponse) {
	r.c = configured(req.ProviderData)
}

func (m *environmentModel) from(e environmentJSON) {
	m.ID, m.ProjectID, m.Name = strID(e.ID), types.Int64Value(e.ProjectID), types.StringValue(e.Name)
	m.Protected, m.RequireApproval = types.BoolValue(e.Protected), types.BoolValue(e.RequireApproval)
}

func (m environmentModel) body() map[string]any {
	b := map[string]any{"name": m.Name.ValueString()}
	if !m.Protected.IsUnknown() && !m.Protected.IsNull() {
		b["protected"] = m.Protected.ValueBool()
	}
	if !m.RequireApproval.IsUnknown() && !m.RequireApproval.IsNull() {
		b["require_approval"] = m.RequireApproval.ValueBool()
	}
	return b
}

func (r *environmentResource) find(ctx context.Context, projectID, id int64) (environmentJSON, error) {
	var p struct {
		Environments []environmentJSON `json:"environments"`
	}
	if err := r.c.do(ctx, "GET", fmt.Sprintf("/api/projects/%d", projectID), nil, &p); err != nil {
		return environmentJSON{}, err
	}
	for _, e := range p.Environments {
		if e.ID == id {
			return e, nil
		}
	}
	return environmentJSON{}, errNotFound
}

func (r *environmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan environmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var e environmentJSON
	if err := r.c.do(ctx, "POST", fmt.Sprintf("/api/projects/%d/environments", plan.ProjectID.ValueInt64()), plan.body(), &e); err != nil {
		resp.Diagnostics.AddError("Create environment", err.Error())
		return
	}
	plan.from(e)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *environmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state environmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	e, err := r.find(ctx, state.ProjectID.ValueInt64(), id64(state.ID))
	if errors.Is(err, errNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Read environment", err.Error())
		return
	}
	state.from(e)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *environmentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state environmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.c.do(ctx, "PATCH", fmt.Sprintf("/api/environments/%d", id64(state.ID)), plan.body(), nil); err != nil {
		resp.Diagnostics.AddError("Update environment", err.Error())
		return
	}
	e, err := r.find(ctx, state.ProjectID.ValueInt64(), id64(state.ID))
	if err != nil {
		resp.Diagnostics.AddError("Read environment", err.Error())
		return
	}
	plan.from(e)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *environmentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state environmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if err := r.c.do(ctx, "DELETE", fmt.Sprintf("/api/environments/%d", id64(state.ID)), nil, nil); err != nil && !errors.Is(err, errNotFound) {
		resp.Diagnostics.AddError("Delete environment", err.Error())
	}
}
