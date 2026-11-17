package supervisor

import (
	"errors"
	"fmt"
	"time"

	"pulso.local/pulso/pkg/types"
)

func cloneFailure(failure *types.ProcessFailure) *types.ProcessFailure {
	if failure == nil {
		return nil
	}
	copy := *failure
	if failure.ExitCode != nil {
		code := *failure.ExitCode
		copy.ExitCode = &code
	}
	return &copy
}

func (p *ManagedProcess) recordFailure(phase string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastFailure = &types.ProcessFailure{Phase: phase, Message: err.Error(), OccurredAt: time.Now(), PID: p.PID}
	p.lastEvent = phase + "_failed"
	p.lastReason = err.Error()
}

// An immediate exit must not be overwritten by a late startup notification.
func (p *ManagedProcess) recordRunning(event, reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.GetState() != types.StateRunning && p.GetState() != types.StateStarting {
		return
	}
	p.lastEvent, p.lastReason = event, reason
}

// FlushLogs makes unterminated lines visible too (many errors omit a final newline).
func (p *ManagedProcess) FlushLogs() error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.flushLogsLocked()
}

func (p *ManagedProcess) flushLogsLocked() error {
	var failures []error
	for _, writer := range []any{p.stdoutWriter, p.stderrWriter} {
		if flusher, ok := writer.(interface{ Flush() error }); ok {
			if err := flusher.Flush(); err != nil {
				failures = append(failures, fmt.Errorf("flush application log: %w", err))
			}
		}
	}
	return errors.Join(failures...)
}
