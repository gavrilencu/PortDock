// Package provider implements the PortDock Terraform provider.
package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type portdockProvider struct{ version string }

// New returns the provider factory.
func New(version string) func() provider.Provider {
	return func() provider.Provider { return &portdockProvider{version: version} }
}

type providerModel struct {
	URL   types.String `tfsdk:"url"`
	Token types.String `tfsdk:"token"`
}

func (p *portdockProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "portdock"
	resp.Version = p.version
}

func (p *portdockProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage PortDock projects, environments, applications, domains and variables.",
		Attributes: map[string]schema.Attribute{
			"url":   schema.StringAttribute{Optional: true, Description: "Address of PortDock, e.g. https://portdock.example.com (or PORTDOCK_URL)."},
			"token": schema.StringAttribute{Optional: true, Sensitive: true, Description: "API token (or PORTDOCK_TOKEN), preferably of a service account. The write scope covers applications, domains and variables; projects and environments need the full scope."},
		},
	}
}

func (p *portdockProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	url, token := os.Getenv("PORTDOCK_URL"), os.Getenv("PORTDOCK_TOKEN")
	if !cfg.URL.IsNull() {
		url = cfg.URL.ValueString()
	}
	if !cfg.Token.IsNull() {
		token = cfg.Token.ValueString()
	}
	if url == "" || token == "" {
		resp.Diagnostics.AddError("PortDock is not configured", "Set url and token in the provider block, or PORTDOCK_URL and PORTDOCK_TOKEN.")
		return
	}
	c := newClient(url, token, p.version)
	resp.ResourceData, resp.DataSourceData = c, c
}

func (p *portdockProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{newProjectResource, newEnvironmentResource, newApplicationResource, newDomainResource, newEnvVarResource}
}

func (p *portdockProvider) DataSources(context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{newServerDataSource}
}

// configured extracts the client from a Configure request.
func configured(data any) *client {
	c, _ := data.(*client)
	return c
}
