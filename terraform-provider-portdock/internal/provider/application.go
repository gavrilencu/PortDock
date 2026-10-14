package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// ---- portdock_application ----

type applicationResource struct{ c *client }

type applicationModel struct {
	ID              types.String `tfsdk:"id"`
	UUID            types.String `tfsdk:"uuid"`
	EnvironmentID   types.Int64  `tfsdk:"environment_id"`
	ServerID        types.Int64  `tfsdk:"server_id"`
	Name            types.String `tfsdk:"name"`
	Description     types.String `tfsdk:"description"`
	DockerImage     types.String `tfsdk:"docker_image"`
	GitRepository   types.String `tfsdk:"git_repository"`
	GitBranch       types.String `tfsdk:"git_branch"`
	BuildPack       types.String `tfsdk:"build_pack"`
	StartCommand    types.String `tfsdk:"start_command"`
	Port            types.Int64  `tfsdk:"port"`
	Replicas        types.Int64  `tfsdk:"replicas"`
	CPULimit        types.String `tfsdk:"cpu_limit"`
	MemoryLimit     types.String `tfsdk:"memory_limit"`
	HealthCheckPath types.String `tfsdk:"health_check_path"`
	AutoDeploy      types.Bool   `tfsdk:"auto_deploy"`
	Status          types.String `tfsdk:"status"`
}

type applicationJSON struct {
	ID              int64  `json:"id"`
	UUID            string `json:"uuid"`
	EnvironmentID   int64  `json:"environment_id"`
	ServerID        int64  `json:"server_id"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	SourceType      string `json:"source_type"`
	DockerImage     string `json:"docker_image"`
	GitRepository   string `json:"git_repository"`
	GitBranch       string `json:"git_branch"`
	BuildPack       string `json:"build_pack"`
	StartCommand    string `json:"start_command"`
	Port            int64  `json:"port"`
	Replicas        int64  `json:"replicas"`
	CPULimit        string `json:"cpu_limit"`
	MemoryLimit     string `json:"memory_limit"`
	HealthCheckPath string `json:"health_check_path"`
	AutoDeploy      bool   `json:"auto_deploy"`
	Status          string `json:"status"`
}

func newApplicationResource() resource.Resource { return &applicationResource{} }

func (r *applicationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_application"
}

func optionalInt(desc string) schema.Int64Attribute {
	return schema.Int64Attribute{Optional: true, Computed: true, Description: desc, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}}
}

func (r *applicationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "An application, deployed from a Git repository or a Docker image. Deployments are triggered by git pushes, the UI, pdctl or the API.",
		Attributes: map[string]schema.Attribute{
			"id":                idAttr(),
			"uuid":              idAttr(),
			"environment_id":    schema.Int64Attribute{Required: true, Description: "Environment of the application."},
			"server_id":         schema.Int64Attribute{Required: true, Description: "Primary server (see the portdock_server data source)."},
			"name":              schema.StringAttribute{Required: true},
			"description":       optionalString(""),
			"docker_image":      schema.StringAttribute{Optional: true, Description: "Image to run, e.g. nginx:alpine. Set either this or git_repository."},
			"git_repository":    schema.StringAttribute{Optional: true, Description: "Repository to build."},
			"git_branch":        optionalString("Branch to deploy (default main)."),
			"build_pack":        optionalString("auto, dockerfile, compose, node, bun, python, go, static, shell or nixpacks."),
			"start_command":     optionalString(""),
			"port":              optionalInt("Port the application listens on."),
			"replicas":          optionalInt("Containers per server."),
			"cpu_limit":         optionalString("e.g. 0.5"),
			"memory_limit":      optionalString("e.g. 512m"),
			"health_check_path": optionalString("e.g. /health"),
			"auto_deploy":       schema.BoolAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()}},
			"status":            idAttr(),
		},
	}
}

func (r *applicationResource) Configure(_ context.Context, req resource.ConfigureRequest, _ *resource.ConfigureResponse) {
	r.c = configured(req.ProviderData)
}

func (r *applicationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (m *applicationModel) from(a applicationJSON) {
	m.ID, m.UUID = strID(a.ID), types.StringValue(a.UUID)
	m.EnvironmentID, m.ServerID = types.Int64Value(a.EnvironmentID), types.Int64Value(a.ServerID)
	m.Name, m.Description = types.StringValue(a.Name), types.StringValue(a.Description)
	if a.SourceType == "image" {
		m.DockerImage, m.GitRepository = types.StringValue(a.DockerImage), types.StringNull()
	} else {
		m.GitRepository, m.DockerImage = types.StringValue(a.GitRepository), types.StringNull()
	}
	m.GitBranch, m.BuildPack, m.StartCommand = types.StringValue(a.GitBranch), types.StringValue(a.BuildPack), types.StringValue(a.StartCommand)
	m.Port, m.Replicas = types.Int64Value(a.Port), types.Int64Value(a.Replicas)
	m.CPULimit, m.MemoryLimit, m.HealthCheckPath = types.StringValue(a.CPULimit), types.StringValue(a.MemoryLimit), types.StringValue(a.HealthCheckPath)
	m.AutoDeploy, m.Status = types.BoolValue(a.AutoDeploy), types.StringValue(a.Status)
}

func (m applicationModel) body() (map[string]any, error) {
	b := map[string]any{"name": m.Name.ValueString(), "environment_id": m.EnvironmentID.ValueInt64(), "server_id": m.ServerID.ValueInt64()}
	img, repo := !m.DockerImage.IsNull() && m.DockerImage.ValueString() != "", !m.GitRepository.IsNull() && m.GitRepository.ValueString() != ""
	switch {
	case img == repo:
		return nil, errors.New("set exactly one of docker_image and git_repository")
	case img:
		b["source_type"], b["build_pack"], b["docker_image"] = "image", "image", m.DockerImage.ValueString()
	default:
		b["source_type"], b["git_repository"] = "git", m.GitRepository.ValueString()
	}
	str := func(k string, v types.String) {
		if !v.IsNull() && !v.IsUnknown() {
			b[k] = v.ValueString()
		}
	}
	num := func(k string, v types.Int64) {
		if !v.IsNull() && !v.IsUnknown() {
			b[k] = v.ValueInt64()
		}
	}
	str("description", m.Description)
	str("git_branch", m.GitBranch)
	if !img {
		str("build_pack", m.BuildPack)
	}
	str("start_command", m.StartCommand)
	num("port", m.Port)
	num("replicas", m.Replicas)
	str("cpu_limit", m.CPULimit)
	str("memory_limit", m.MemoryLimit)
	str("health_check_path", m.HealthCheckPath)
	if !m.AutoDeploy.IsNull() && !m.AutoDeploy.IsUnknown() {
		b["auto_deploy"] = m.AutoDeploy.ValueBool()
	}
	return b, nil
}

func (r *applicationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan applicationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body, err := plan.body()
	if err != nil {
		resp.Diagnostics.AddError("Invalid application", err.Error())
		return
	}
	var a applicationJSON
	if err := r.c.do(ctx, "POST", "/api/applications", body, &a); err != nil {
		resp.Diagnostics.AddError("Create application", err.Error())
		return
	}
	plan.from(a)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *applicationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state applicationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var a applicationJSON
	if err := r.c.do(ctx, "GET", fmt.Sprintf("/api/applications/%d", id64(state.ID)), nil, &a); err != nil {
		if errors.Is(err, errNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read application", err.Error())
		return
	}
	state.from(a)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *applicationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state applicationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body, err := plan.body()
	if err != nil {
		resp.Diagnostics.AddError("Invalid application", err.Error())
		return
	}
	var a applicationJSON
	if err := r.c.do(ctx, "PATCH", fmt.Sprintf("/api/applications/%d", id64(state.ID)), body, &a); err != nil {
		resp.Diagnostics.AddError("Update application", err.Error())
		return
	}
	plan.from(a)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *applicationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state applicationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if err := r.c.do(ctx, "DELETE", fmt.Sprintf("/api/applications/%d", id64(state.ID)), nil, nil); err != nil && !errors.Is(err, errNotFound) {
		resp.Diagnostics.AddError("Delete application", err.Error())
	}
}
