package main

import (
	"fmt"
	"os"

	flag "github.com/spf13/pflag"

	"github.com/render-oss/render-mcp-server/cmd"
	"github.com/render-oss/render-mcp-server/pkg/cfg"
)

func main() {
	versionFlag := flag.Bool("version", false, "Print version information and exit")
	flag.BoolVar(versionFlag, "v", false, "Print version information and exit")

	var transport string
	flag.StringVarP(&transport, "transport", "t", "stdio", "Transport type (stdio or http)")

	var mode string
	flag.StringVarP(&mode, "mode", "m", "both", "Tool mode: sre (new tools), api (legacy 24 tools), both (all)")

	flag.Parse()

	if *versionFlag {
		fmt.Println("render-mcp-server version", cfg.Version)
		os.Exit(0)
	}

	cmd.Serve(transport, mode)
}
