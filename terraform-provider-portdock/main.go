// Command terraform-provider-portdock is the Terraform provider for PortDock:
// projects, environments, applications, domains and variables as code.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/portdock/terraform-provider-portdock/internal/provider"
)

var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run the provider with support for debuggers")
	flag.Parse()
	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/portdock/portdock",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err)
	}
}
