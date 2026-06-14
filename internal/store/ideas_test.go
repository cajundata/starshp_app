package store

import "testing"

func TestPipelineSchemaTablesExist(t *testing.T) {
	st := openTestStore(t)
	tables := []string{
		"ideas", "idea_status_history", "idea_reviews",
		"idea_review_roles", "kill_criteria", "send_backs",
	}
	for _, name := range tables {
		var got string
		err := st.db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&got)
		if err != nil {
			t.Fatalf("table %q missing: %v", name, err)
		}
	}
}
