package assignment

import (
	"encoding/json"
	"testing"

	"github.com/xeipuuv/gojsonschema"
)

func validate(t *testing.T, schema json.RawMessage, doc string) *gojsonschema.Result {
	t.Helper()
	s, err := gojsonschema.NewSchema(gojsonschema.NewBytesLoader(schema))
	if err != nil {
		t.Fatalf("schema invalid: %v", err)
	}
	res, err := s.Validate(gojsonschema.NewStringLoader(doc))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func firstMC(t *testing.T) Question {
	t.Helper()
	loaded, err := Load(testdataDir(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range loaded.Questions {
		if q.Type == TypeMultipleChoice {
			return q
		}
	}
	t.Fatal("no MC question loaded")
	return Question{}
}

func TestBuildSubmitAnswerSchema_MC_BoundsIndex(t *testing.T) {
	mc := firstMC(t) // 4 choices -> 0..3
	schema := BuildSubmitAnswerSchema(mc)
	if !validate(t, schema, `{"confidence":"high","answerIndex":3}`).Valid() {
		t.Error("index 3 should be valid for a 4-choice question")
	}
	if validate(t, schema, `{"confidence":"high","answerIndex":4}`).Valid() {
		t.Error("index 4 should be rejected for a 4-choice question")
	}
	if validate(t, schema, `{"answerIndex":0}`).Valid() {
		t.Error("missing confidence should be rejected")
	}
}

func TestBuildSubmitAnswerSchema_Worksheet_EnumeratesCellKeys(t *testing.T) {
	q := loadWorksheet(t)
	schema := BuildSubmitAnswerSchema(q)
	good := `{"confidence":"medium","cells":[{"id":"0::0_table0_cell_c2_r0","value":"54800"}]}`
	if !validate(t, schema, good).Valid() {
		t.Error("a real answerable cell key should be accepted")
	}
	bad := `{"confidence":"medium","cells":[{"id":"not_a_real_key","value":"1"}]}`
	if validate(t, schema, bad).Valid() {
		t.Error("an unknown cell key should be rejected")
	}
}

func TestBuildSubmitAnswerSchema_FlagVocabulary(t *testing.T) {
	mc := firstMC(t)
	schema := BuildSubmitAnswerSchema(mc)
	ok := `{"confidence":"low","answerIndex":0,"flags":[{"code":"missing_information","detail":"no rate given"}]}`
	if !validate(t, schema, ok).Valid() {
		t.Error("known flag code should be accepted")
	}
	bad := `{"confidence":"low","answerIndex":0,"flags":[{"code":"banana","detail":"x"}]}`
	if validate(t, schema, bad).Valid() {
		t.Error("unknown flag code should be rejected")
	}
}
