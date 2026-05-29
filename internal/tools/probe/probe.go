// Package probe provides a configurable test-only Tool implementation used by
// registry tests and loop tests.
package probe

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/cajundata/starshp_app/internal/tools"
)

type Probe struct {
	name   string
	schema json.RawMessage

	Delay time.Duration
	Out   string
	Meta  json.RawMessage
	Err   error

	mu        sync.Mutex
	lastCtx   tools.ExecContext
	callCount int
}

func New(name, schema string) *Probe {
	return &Probe{name: name, schema: json.RawMessage(schema), Out: "ok"}
}

func (p *Probe) Name() string                 { return p.name }
func (p *Probe) Description() string          { return "test probe" }
func (p *Probe) InputSchema() json.RawMessage { return p.schema }
func (p *Probe) Timeout() time.Duration       { return 0 }

func (p *Probe) Execute(ctx context.Context, ec tools.ExecContext, _ json.RawMessage) (tools.ExecResult, error) {
	p.mu.Lock()
	p.lastCtx = ec
	p.callCount++
	p.mu.Unlock()
	if p.Delay > 0 {
		select {
		case <-time.After(p.Delay):
		case <-ctx.Done():
			return tools.ExecResult{}, ctx.Err()
		}
	}
	if p.Err != nil {
		return tools.ExecResult{}, p.Err
	}
	return tools.ExecResult{Output: p.Out, Metadata: p.Meta}, nil
}

func (p *Probe) LastExecContext() tools.ExecContext {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastCtx
}

func (p *Probe) CallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.callCount
}
