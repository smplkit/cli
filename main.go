// smplkit is the official command-line interface for the smplkit
// platform. It is a thin imperative shell over the Go SDK's management
// client — every command maps onto a Manage().<Ns>().<Verb> call.
//
// See ADR-053 for design notes.
package main

import (
	"fmt"
	"os"

	"github.com/smplkit/cli/cmd"
)

// version is stamped by GoReleaser at build time (-ldflags). The default
// is read from `go install` builds where the linker flag is absent.
var version = "dev"

func main() {
	if err := cmd.Execute(version); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
