package companion

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"sync"
	"time"
)

// traceSilenceWindow mirrors the frontend's TRACE_TIMEOUT_MS: a trace is only
// considered done once no further partial echo has arrived for this long.
const traceSilenceWindow = 5 * time.Second

// traceEcho is one overheard trace packet (partial or final) for a tag, fed
// in by the PayloadTypeTrace handler in recv.go.
type traceEcho struct {
	hops    int
	pathHex []string
	hopSNRs []float64
	snr     *float64
}

// TraceOutcome is the result of one awaited trace, returned by RunTrace.
type TraceOutcome struct {
	Tag      uint32
	HopSNRs  []float64 // real dB; may be partial if the silence window lapsed first
	PathHex  []string
	SNR      *float64 // SNR at which we received the final returning packet
	Elapsed  time.Duration
	Complete bool // len(HopSNRs) >= the expected hop count before timing out
}

// RunTrace sends one trace over path/pathHashSize and blocks until the echo
// path completes (an echo reports at least as many hops as the path implies)
// or traceSilenceWindow elapses with no further partial echo. The waiter is
// registered before the packet is transmitted so a fast echo can never race
// ahead of registration. Returns a non-nil outcome with Complete=false on
// timeout; an error is returned only for validation/send failures.
func (c *Companion) RunTrace(ctx context.Context, path []byte, pathHashSize uint8) (*TraceOutcome, error) {
	if len(path) == 0 {
		return nil, fmt.Errorf("path is required")
	}
	if pathHashSize != 1 && pathHashSize != 2 && pathHashSize != 4 {
		return nil, fmt.Errorf("pathHashSize must be 1, 2, or 4")
	}
	if len(path)%int(pathHashSize) != 0 {
		return nil, fmt.Errorf("path length %d is not divisible by pathHashSize %d", len(path), pathHashSize)
	}
	expected := len(path) / int(pathHashSize)

	var tagBytes [4]byte
	if _, err := rand.Read(tagBytes[:]); err != nil {
		return nil, fmt.Errorf("generating trace tag: %w", err)
	}
	tag := binary.LittleEndian.Uint32(tagBytes[:])

	var authBytes [4]byte
	if _, err := rand.Read(authBytes[:]); err != nil {
		return nil, fmt.Errorf("generating trace auth: %w", err)
	}
	auth := binary.LittleEndian.Uint32(authBytes[:])

	ch := c.registerTraceWaiter(tag)
	defer c.deregisterTraceWaiter(tag)

	start := time.Now()
	if err := c.sendTracePacket(tag, auth, path, pathHashSize); err != nil {
		return nil, err
	}

	timer := time.NewTimer(traceSilenceWindow)
	defer timer.Stop()

	// Seed non-nil empty slices: nil would json-marshal as `null`, not `[]`,
	// crashing JS consumers that .forEach() it.
	last := traceEcho{hopSNRs: []float64{}, pathHex: []string{}}
	for {
		select {
		case echo := <-ch:
			last = echo
			hops := echo.hops
			if hops <= 0 {
				hops = expected
			}
			if len(echo.hopSNRs) >= hops {
				return &TraceOutcome{
					Tag:      tag,
					HopSNRs:  echo.hopSNRs,
					PathHex:  echo.pathHex,
					SNR:      echo.snr,
					Elapsed:  time.Since(start),
					Complete: true,
				}, nil
			}
			if !timer.Stop() {
				<-timer.C
			}
			timer.Reset(traceSilenceWindow)
		case <-timer.C:
			return &TraceOutcome{
				Tag:      tag,
				HopSNRs:  last.hopSNRs,
				PathHex:  last.pathHex,
				SNR:      last.snr,
				Elapsed:  time.Since(start),
				Complete: false,
			}, nil
		case <-ctx.Done():
			return &TraceOutcome{
				Tag:      tag,
				HopSNRs:  last.hopSNRs,
				PathHex:  last.pathHex,
				SNR:      last.snr,
				Elapsed:  time.Since(start),
				Complete: false,
			}, nil
		}
	}
}

func (c *Companion) registerTraceWaiter(tag uint32) chan traceEcho {
	c.traceWaiters.Lock()
	defer c.traceWaiters.Unlock()
	if c.traceWaiters.byTag == nil {
		c.traceWaiters.byTag = make(map[uint32]chan traceEcho)
	}
	ch := make(chan traceEcho, 8)
	c.traceWaiters.byTag[tag] = ch
	return ch
}

func (c *Companion) deregisterTraceWaiter(tag uint32) {
	c.traceWaiters.Lock()
	defer c.traceWaiters.Unlock()
	delete(c.traceWaiters.byTag, tag)
}

// notifyTraceWaiter is called from the RX dispatch goroutine (recv.go) for
// every overheard trace echo. It never blocks the caller.
func (c *Companion) notifyTraceWaiter(tag uint32, echo traceEcho) {
	c.traceWaiters.Lock()
	ch, ok := c.traceWaiters.byTag[tag]
	c.traceWaiters.Unlock()
	if !ok {
		return
	}
	select {
	case ch <- echo:
	default:
	}
}

// traceWaiters holds in-flight RunTrace callers keyed by trace tag.
type traceWaiters struct {
	sync.Mutex
	byTag map[uint32]chan traceEcho
}
