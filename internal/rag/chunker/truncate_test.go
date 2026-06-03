package chunker

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTruncateToTokens_CapsOversizedInput(t *testing.T) {
	// Build text well over the cap. "revenue " is a couple of tokens; repeating
	// it many times guarantees we exceed any small maxTokens budget.
	long := strings.Repeat("revenue recognition ", 5000)

	before, err := CountTokens(long)
	require.NoError(t, err)
	require.Greater(t, before, 1000, "fixture should exceed the cap under test")

	out, truncated, err := TruncateToTokens(long, 1000)
	require.NoError(t, err)
	assert.True(t, truncated, "oversized input should report truncation")

	got, err := CountTokens(out)
	require.NoError(t, err)
	assert.LessOrEqual(t, got, 1000, "output must not exceed maxTokens")
}

func TestTruncateToTokens_PassesShortInputUnchanged(t *testing.T) {
	in := "What is revenue recognition?"
	out, truncated, err := TruncateToTokens(in, 1000)
	require.NoError(t, err)
	assert.False(t, truncated)
	assert.Equal(t, in, out)
}

func TestTruncateToTokens_RejectsNonPositiveBudget(t *testing.T) {
	_, _, err := TruncateToTokens("revenue", 0)
	assert.Error(t, err)
}
