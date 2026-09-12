// Command web-fetch fetches one URL from this machine and returns its
// extracted body as text, as a CLI and as a local MCP server. It is a
// primitive: no credentials, no search, no summary. The caller bounds the
// response and the runtime decides where a large result lives.
package main

import (
	"os"

	"github.com/nlink-jp/web-fetch/internal/app"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(app.Run(os.Args[1:], version))
}
