package journal

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// Interruptible returns a context canceled when the process is interrupted,
// with a cause naming the signal, plus a stop function to release the
// handler.
//
// It exists so that a signal becomes a *reason* rather than just an ending.
// [signal.NotifyContext] cancels with the anonymous context.Canceled, and a
// run parked with "context canceled" tells the next reader nothing about
// whether a person pressed ctrl-c, a supervisor cycled the host, or a
// deadline elapsed. Here the cause is the sentence the park will quote.
//
// The intended shape of an orchestrator is:
//
//	ctx, stop := journal.Interruptible(ctx)
//	defer stop()
//
//	r, err := store.Create(meta)
//	if err != nil { return err }
//	defer r.Close()
//
//	if err := loop(ctx, r); err != nil {
//		if ctx.Err() != nil {
//			return r.Parked(context.Cause(ctx).Error())
//		}
//		return r.Parked(err.Error())
//	}
//
// The deferred Close is not redundant with the explicit park: it covers the
// paths nobody wrote a park for — a panic, an early return added later, a
// branch that forgot. Signals are the deaths a process can observe; SIGKILL
// and power loss are not, and those are what the lock and [Store.Reopen]
// answer instead.
func Interruptible(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(parent)
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		defer signal.Stop(ch)
		select {
		case sig := <-ch:
			cancel(fmt.Errorf("interrupted by %s", sig))
		case <-ctx.Done():
		}
	}()
	return ctx, func() { cancel(context.Canceled) }
}
