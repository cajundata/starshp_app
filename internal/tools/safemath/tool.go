package safemath

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cajundata/starshp_app/internal/tools"
)

const description = `Deterministic decimal arithmetic. Use for any non-trivial calculation — tax computations, present value, percentages, subtotals — to verify your work. Supports + - * / ^, parentheses, unary minus, percent suffix (22% = 0.22), and functions min, max, abs, round (round(x) and round(x, places) both use banker's rounding), sqrt, floor, ceil. Decimal-precise. Not for symbolic algebra, variables, or units.`

const inputSchema = `{
  "type": "object",
  "properties": {
    "expression": {"type": "string", "minLength": 1, "maxLength": 1000}
  },
  "required": ["expression"],
  "additionalProperties": false
}`

type Tool struct{}

func New() *Tool { return &Tool{} }

func (Tool) Name() string                 { return "safe_math" }
func (Tool) Description() string          { return description }
func (Tool) InputSchema() json.RawMessage { return json.RawMessage(inputSchema) }
func (Tool) Timeout() time.Duration       { return 5 * time.Second }

func (Tool) Execute(_ context.Context, _ tools.ExecContext, input json.RawMessage) (tools.ExecResult, error) {
	var args struct {
		Expression string `json:"expression"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return tools.ExecResult{}, fmt.Errorf("safe_math: invalid input json: %w", err)
	}
	expr := strings.TrimSpace(args.Expression)
	node, err := Parse(expr)
	if err != nil {
		return tools.ExecResult{}, fmt.Errorf("safe_math: %s", err.Error())
	}
	d, err := Eval(node)
	if err != nil {
		return tools.ExecResult{}, fmt.Errorf("safe_math: %s", err.Error())
	}
	result := d.String()
	sum := sha256.Sum256([]byte(result))
	meta, _ := json.Marshal(struct {
		Norm   string `json:"normalized_expression"`
		Result string `json:"result_decimal_string"`
		Hash   string `json:"result_hash"`
	}{Norm: expr, Result: result, Hash: hex.EncodeToString(sum[:])})
	return tools.ExecResult{Output: result, Metadata: meta}, nil
}
