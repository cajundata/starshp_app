// Package fakeprovider is a scripted provider.ChatProvider for loop tests. It
// emits a canned sequence of Delta slices — one slice per Stream call — so a
// test can drive the agentic loop through a deterministic multi-iteration
// script without touching a real model.
package fakeprovider

import (
	"context"
	"errors"

	"github.com/cajundata/starshp_app/internal/provider"
)

// Scripted returns the Nth canned iteration on the Nth call to Stream. It
// captures every request it received in Reqs and, if set, invokes Hook before
// returning each iteration so tests can assert on what the loop sent.
type Scripted struct {
	Iterations [][]provider.Delta
	Hook       func(call int, req provider.ChatRequest) // observation hook (optional)

	Reqs  []provider.ChatRequest // every request received, in call order
	calls int
}

// Stream emits the next canned iteration. Calls are sequential within the loop
// (the loop awaits each stream fully before issuing the next), so no locking is
// required. Returns an error once the script is exhausted, which surfaces in
// the loop as a provider stream error.
func (s *Scripted) Stream(_ context.Context, req provider.ChatRequest) (<-chan provider.Delta, error) {
	s.Reqs = append(s.Reqs, req)
	if s.calls >= len(s.Iterations) {
		return nil, errors.New("fakeprovider: out of canned iterations")
	}
	if s.Hook != nil {
		s.Hook(s.calls, req)
	}
	deltas := s.Iterations[s.calls]
	s.calls++
	ch := make(chan provider.Delta, len(deltas))
	for _, d := range deltas {
		ch <- d
	}
	close(ch)
	return ch, nil
}

// Calls reports how many times Stream has been invoked.
func (s *Scripted) Calls() int { return s.calls }
