// Terraform provider for Lucidity (cloud storage optimization platform).
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/Devaansh/terraform-provider-lucidity/internal/provider"
)

// version is overridden at build time via -ldflags "-X main.version=..."
// (wired up by GoReleaser); local `go build`/`go run` gets "dev".
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "set to true to run the provider with support for debuggers like delve")
	flag.Parse()

	opts := providerserver.ServeOpts{
		// TODO: update to the final registry namespace before publishing.
		Address: "registry.terraform.io/Devaansh/lucidity",
		Debug:   debug,
	}

	if err := providerserver.Serve(context.Background(), provider.New(version), opts); err != nil {
		log.Fatal(err.Error())
	}
}
