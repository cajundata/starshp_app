// Package tools is the in-process tool registry used by chat.Service's
// agentic loop. Tools depend on this package; this package depends on
// internal/chat for ExecContext-adjacent types (RetrievalMode), but does
// not depend on internal/store or internal/appapi.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cajundata/starshp_app/internal/chat"
	"github.com/cajundata/starshp_app/internal/provider"
	"github.com/xeipuuv/gojsonschema"
)

// ExecContext carries conversation-scoped state into tool execution.
type ExecContext struct {
	ConversationID string
	TurnID         string
	RunID          string
	RetrievalMode  chat.RetrievalMode
	TextbookScope  []string // book names only; richer scope via chat.ScopeResolver
}

// ExecResult is what a tool returns. Output is the exact text the model
// sees; Metadata is JSON persisted on the tool_result event.
type ExecResult struct {
	Output   string
	Metadata json.RawMessage
}

// Tool is the registry interface. Implementations must be safe for
// concurrent use across multiple in-flight runs.
type Tool interface {
	Name() string
	Description() string
	InputSchema() json.RawMessage
	Execute(ctx context.Context, ec ExecContext, input json.RawMessage) (ExecResult, error)
	Timeout() time.Duration // 0 -> use registry default
}

// Normalized tool-result error codes (distinct from provider AppError codes).
const (
	ErrCodeUnknownTool      = "unknown_tool"
	ErrCodeSchemaValidation = "schema_validation_error"
	ErrCodeTimeout          = "timeout"
	ErrCodeExecution        = "execution_error"
)

type Registry struct {
	defaultTimeout time.Duration
	tools          map[string]Tool
	schemas        map[string]*gojsonschema.Schema
}

func NewRegistry(defaultTimeout time.Duration) *Registry {
	if defaultTimeout <= 0 {
		defaultTimeout = 30 * time.Second
	}
	return &Registry{
		defaultTimeout: defaultTimeout,
		tools:          map[string]Tool{},
		schemas:        map[string]*gojsonschema.Schema{},
	}
}

func (r *Registry) Register(t Tool) error {
	name := t.Name()
	if name == "" {
		return errors.New("tool name must be non-empty")
	}
	if _, dup := r.tools[name]; dup {
		return fmt.Errorf("tool already registered: %q", name)
	}
	loader := gojsonschema.NewBytesLoader(t.InputSchema())
	schema, err := gojsonschema.NewSchema(loader)
	if err != nil {
		return fmt.Errorf("invalid input schema for %q: %w", name, err)
	}
	r.tools[name] = t
	r.schemas[name] = schema
	return nil
}

func (r *Registry) Catalog() []provider.ToolDef {
	out := make([]provider.ToolDef, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, provider.ToolDef{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.InputSchema(),
		})
	}
	return out
}

// Execute normalizes all failures into is_error=true tool results so the
// model can see and adapt. The Go error return is reserved for failures
// that should not be exposed to the model (currently none — all
// classified failures land in the tool-result path).
func (r *Registry) Execute(
	ctx context.Context, ec ExecContext, name string, input json.RawMessage,
) (ExecResult, bool, time.Duration, error) {
	start := time.Now()
	t, ok := r.tools[name]
	if !ok {
		return ExecResult{
			Output:   fmt.Sprintf("Unknown tool %q.", name),
			Metadata: errorMetadata(ErrCodeUnknownTool, "tool not registered"),
		}, true, time.Since(start), nil
	}
	// Schema validation before execution.
	res, err := r.schemas[name].Validate(gojsonschema.NewBytesLoader(input))
	if err != nil {
		return ExecResult{
			Output:   fmt.Sprintf("Schema check failed: %s", err.Error()),
			Metadata: errorMetadata(ErrCodeSchemaValidation, err.Error()),
		}, true, time.Since(start), nil
	}
	if !res.Valid() {
		var sb strings.Builder
		for _, e := range res.Errors() {
			sb.WriteString("- ")
			sb.WriteString(e.String())
			sb.WriteString("\n")
		}
		return ExecResult{
			Output:   "Input did not match tool schema:\n" + sb.String(),
			Metadata: errorMetadata(ErrCodeSchemaValidation, sb.String()),
		}, true, time.Since(start), nil
	}
	// Apply timeout.
	timeout := t.Timeout()
	if timeout <= 0 {
		timeout = r.defaultTimeout
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, execErr := t.Execute(execCtx, ec, input)
	latency := time.Since(start)
	if execErr != nil {
		if errors.Is(execErr, context.DeadlineExceeded) || errors.Is(execErr, context.Canceled) {
			// Surface user cancel up; surface timeout as tool-result error.
			if execCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
				return ExecResult{
					Output:   fmt.Sprintf("Tool %q timed out after %s.", name, timeout),
					Metadata: errorMetadata(ErrCodeTimeout, timeout.String()),
				}, true, latency, nil
			}
			return ExecResult{}, false, latency, execErr
		}
		return ExecResult{
			Output:   fmt.Sprintf("Tool %q failed: %s", name, execErr.Error()),
			Metadata: errorMetadata(ErrCodeExecution, execErr.Error()),
		}, true, latency, nil
	}
	return out, false, latency, nil
}

func errorMetadata(code, message string) json.RawMessage {
	type m struct {
		Code    string `json:"error_code"`
		Message string `json:"error_message"`
	}
	b, _ := json.Marshal(m{Code: code, Message: message})
	return b
}
