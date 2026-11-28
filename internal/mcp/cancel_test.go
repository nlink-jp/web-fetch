package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/nlink-jp/mcp/client"
)

// notifications/cancelled reaches the tool's handler: its ctx ends, so the
// upstream request it carries stops instead of running to its end after the
// caller has gone.
func TestACancelledCallEndsTheHandlersCtx(t *testing.T) {
	ended := make(chan error, 1)
	wait := Tool{
		Name: "wait", Description: "Waits for its ctx.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		Handle: func(ctx context.Context, _ json.RawMessage) (any, error) {
			select {
			case <-ctx.Done():
				ended <- ctx.Err()
				return nil, ctx.Err()
			case <-time.After(5 * time.Second):
				ended <- nil
				return "done", nil
			}
		},
	}
	cl := openWith(t, func(ctx context.Context, in io.Reader, w io.Writer) error {
		return New("test", wait).Serve(ctx, in, w)
	}, client.Options{CancelNotice: true})
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := cl.CallTool(ctx, "wait", nil, nil); err == nil {
		t.Fatal("the cancelled call returned a result")
	}
	select {
	case err := <-ended:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("the handler's ctx ended with %v, want context.Canceled", err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("the handler never returned")
	}
}
