// Package eval holds the lightweight, Go-tests-only eval harness for the
// agentic loop: a scripted fake provider (subpackage fakeprovider), a capturing
// event sink, loop-level integration tests, and API-key-gated quality fixtures.
package eval

import "github.com/cajundata/starshp_app/internal/chat"

// CaptureSink records every SinkEvent the loop emits so a test can assert on
// the lifecycle sequence after a Send completes.
type CaptureSink struct{ Events []chat.SinkEvent }

func (c *CaptureSink) Emit(e chat.SinkEvent) { c.Events = append(c.Events, e) }

// Kinds returns just the event kinds, in emission order.
func (c *CaptureSink) Kinds() []chat.SinkEventKind {
	out := make([]chat.SinkEventKind, len(c.Events))
	for i, e := range c.Events {
		out[i] = e.Kind
	}
	return out
}

// Has reports whether an event of the given kind was emitted.
func (c *CaptureSink) Has(k chat.SinkEventKind) bool {
	for _, e := range c.Events {
		if e.Kind == k {
			return true
		}
	}
	return false
}
