// Package clientfactory builds smplkit.SmplClient instances
// from the CLI's global flags. Only explicitly-set flag values flow
// into Config — every other field is left empty so the SDK's
// own resolveConfig chain (defaults → ~/.smplkit → SMPLKIT_* env vars
// → explicit) applies unchanged. The CLI does not duplicate that
// resolution.
package clientfactory

import (
	"fmt"

	smplkit "github.com/smplkit/go-sdk/v3"

	"github.com/smplkit/cli/internal/cliconfig"
)

// New returns a SmplClient with credentials/endpoints sourced
// from the SDK's resolver. The CLI only forwards globals that the user
// supplied on the command line — leaving the others empty lets the SDK
// fall through to ~/.smplkit and SMPLKIT_*.
func New(g cliconfig.Globals) (*smplkit.SmplClient, error) {
	cfg := smplkit.Config{
		APIKey:     g.APIKey,
		Profile:    g.Profile,
		BaseDomain: g.BaseDomain,
		Scheme:     g.Scheme,
	}
	client, err := smplkit.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("client: %w", err)
	}
	return client, nil
}
