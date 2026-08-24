package app

import (
	"context"
	"fmt"

	"github.com/meshcore-go/OwlShack/internal/signaltest"
)

// newSignalTestTracer adapts the companion runtime's blocking RunTrace into
// the signaltest.Tracer seam, resolving the companion by name through the
// reload-surviving registry — the same pattern the monitor collectors use —
// so signaltest never imports internal/node/companion.
func newSignalTestTracer(reg *companionRegistry) signaltest.Tracer {
	return func(ctx context.Context, name string, path []byte, hashSize uint8) (*signaltest.TraceResult, error) {
		c, ok := reg.find(name)
		if !ok {
			return nil, fmt.Errorf("companion %q is not running", name)
		}
		out, err := c.RunTrace(ctx, path, hashSize)
		if err != nil {
			return nil, err
		}
		return &signaltest.TraceResult{
			HopSNRs:   out.HopSNRs,
			SNR:       out.SNR,
			ElapsedMs: out.Elapsed.Milliseconds(),
			Complete:  out.Complete,
		}, nil
	}
}
