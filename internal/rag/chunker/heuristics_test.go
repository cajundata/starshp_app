package chunker

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDetectChunkType_Table(t *testing.T) {
	content := "Some intro text.\n| Header1 | Header2 |\n|---------|----------|\n| val1    | val2     |\n| val3    | val4     |\n"
	assert.Equal(t, "table", DetectChunkType(content))
}

func TestDetectChunkType_Example(t *testing.T) {
	content := "Example 3-1: Recording Depreciation\n\nThis example shows how to record depreciation expense."
	assert.Equal(t, "example", DetectChunkType(content))
}

func TestDetectChunkType_ExampleIllustration(t *testing.T) {
	content := "Illustration of the accounting cycle for a service company."
	assert.Equal(t, "example", DetectChunkType(content))
}

func TestDetectChunkType_Rule(t *testing.T) {
	content := "Rule for Revenue Recognition\n\n1. Identify the contract.\n2. Identify performance obligations.\n3. Determine the transaction price."
	assert.Equal(t, "rule", DetectChunkType(content))
}

func TestDetectChunkType_RuleNumberedList(t *testing.T) {
	content := "Steps to follow:\n1. First step in the process.\n2. Second step in the process.\n3. Third step in the process.\n4. Fourth step."
	assert.Equal(t, "rule", DetectChunkType(content))
}

func TestDetectChunkType_ExerciseSupport(t *testing.T) {
	content := "This exercise requires you to prepare journal entries for the following transactions."
	assert.Equal(t, "exercise-support", DetectChunkType(content))
}

func TestDetectChunkType_ExercisePractice(t *testing.T) {
	content := "Practice problems for chapter review. Complete the following homework assignment."
	assert.Equal(t, "exercise-support", DetectChunkType(content))
}

func TestDetectChunkType_Concept(t *testing.T) {
	content := "Accrual accounting recognizes revenue when earned and expenses when incurred, regardless of when cash changes hands."
	assert.Equal(t, "concept", DetectChunkType(content))
}

func TestDetectChunkType_CaseInsensitive(t *testing.T) {
	content := "EXAMPLE of a journal entry for prepaid insurance."
	assert.Equal(t, "example", DetectChunkType(content))
}

func TestDetectChunkType_PriorityTableOverExample(t *testing.T) {
	// Content has both table markers AND "example" keyword
	// Table should win (higher priority)
	content := "Example table of accounts:\n| Account | Type |\n|---------|------|\n| Cash    | Asset |\n| Revenue | Income |\n"
	assert.Equal(t, "table", DetectChunkType(content))
}

func TestDetectChunkType_PriorityExampleOverRule(t *testing.T) {
	// Content has both "example" and "rule" keywords
	// Example should win (higher priority)
	content := "Example of the matching rule in practice. The principle states revenue is recognized when earned."
	assert.Equal(t, "example", DetectChunkType(content))
}

func TestDetectChunkType_EmptyContent(t *testing.T) {
	assert.Equal(t, "concept", DetectChunkType(""))
}
