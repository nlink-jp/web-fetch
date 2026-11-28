package mcp

import (
	"context"
	"io"
	"testing"

	"github.com/nlink-jp/mcp/client"
)

func echoServe(ctx context.Context, in io.Reader, w io.Writer) error {
	return New("test", echoTool(nil)).Serve(ctx, in, w)
}

// ADR-030 §7: the server against the three probe clients (legacy only,
// dual-era, modern only).
func TestAgainstTheProbeClients(t *testing.T) {
	probeMatrix(t, echoServe)
}

// The client that opens the server receives the instructions, whichever era
// it opens in.
func TestOpeningCarriesTheInstructions(t *testing.T) {
	cl := openWith(t, echoServe, client.Options{})
	if got := cl.Instructions(); got != Instructions {
		t.Errorf("instructions = %q, want the package's Instructions", got)
	}
}
