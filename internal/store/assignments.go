package store

import (
	"database/sql"
	"encoding/json"
	"time"
)

type Assignment struct {
	ID             string
	SourceDir      string
	Title          string
	ManifestHash   string
	Model          string
	GroundingScope string
	Status         string
	TotalItems     int
	CreatedAt      int64
	UpdatedAt      int64
}

type AssignmentItem struct {
	ID             string
	AssignmentID   string
	Seq            int
	SourcePath     string
	Type           string
	Title          string
	RunID          string
	ConversationID string
	Status         string
	Confidence     string
	AnswerJSON     string
	FlagsJSON      string
	AnswerPath     string
	Error          string
	CreatedAt      int64
	UpdatedAt      int64
}

func (s *Store) CreateAssignment(a Assignment) error {
	now := time.Now().UnixMilli()
	_, err := s.db.Exec(
		`INSERT INTO assignments
            (id, source_dir, title, manifest_hash, model, grounding_scope,
             status, total_items, created_at, updated_at)
         VALUES (?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.SourceDir, a.Title, a.ManifestHash, a.Model, nullIfEmpty(a.GroundingScope),
		a.Status, a.TotalItems, now, now)
	return err
}

func (s *Store) UpdateAssignmentStatus(id, status string) error {
	_, err := s.db.Exec(`UPDATE assignments SET status=?, updated_at=? WHERE id=?`,
		status, time.Now().UnixMilli(), id)
	return err
}

func (s *Store) CreateAssignmentItem(it AssignmentItem) error {
	now := time.Now().UnixMilli()
	_, err := s.db.Exec(
		`INSERT INTO assignment_items
            (id, assignment_id, seq, source_path, type, title, run_id,
             conversation_id, status, confidence, answer_json, flags_json,
             answer_path, error, created_at, updated_at)
         VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		it.ID, it.AssignmentID, it.Seq, it.SourcePath, it.Type, nullIfEmpty(it.Title),
		nullIfEmpty(it.RunID), nullIfEmpty(it.ConversationID), it.Status,
		nullIfEmpty(it.Confidence), nullIfEmpty(it.AnswerJSON), nullIfEmpty(it.FlagsJSON),
		nullIfEmpty(it.AnswerPath), nullIfEmpty(it.Error), now, now)
	return err
}

func (s *Store) UpdateAssignmentItem(it AssignmentItem) error {
	_, err := s.db.Exec(
		`UPDATE assignment_items
            SET status=?, confidence=?, answer_json=?, flags_json=?,
                answer_path=?, error=?, run_id=?, conversation_id=?, updated_at=?
          WHERE id=?`,
		it.Status, nullIfEmpty(it.Confidence), nullIfEmpty(it.AnswerJSON),
		nullIfEmpty(it.FlagsJSON), nullIfEmpty(it.AnswerPath), nullIfEmpty(it.Error),
		nullIfEmpty(it.RunID), nullIfEmpty(it.ConversationID),
		time.Now().UnixMilli(), it.ID)
	return err
}

func (s *Store) GetAssignment(id string) (Assignment, error) {
	var a Assignment
	err := s.db.QueryRow(
		`SELECT id, source_dir, title, manifest_hash, model,
                COALESCE(grounding_scope,''), status, total_items, created_at, updated_at
           FROM assignments WHERE id=?`, id).Scan(
		&a.ID, &a.SourceDir, &a.Title, &a.ManifestHash, &a.Model,
		&a.GroundingScope, &a.Status, &a.TotalItems, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}

// FindAssignmentByManifest returns the most recent assignment for a source dir
// + manifest hash, or ok=false if none exists.
func (s *Store) FindAssignmentByManifest(sourceDir, manifestHash string) (Assignment, bool, error) {
	var a Assignment
	err := s.db.QueryRow(
		`SELECT id, source_dir, title, manifest_hash, model,
                COALESCE(grounding_scope,''), status, total_items, created_at, updated_at
           FROM assignments WHERE source_dir=? AND manifest_hash=?
          ORDER BY created_at DESC LIMIT 1`, sourceDir, manifestHash).Scan(
		&a.ID, &a.SourceDir, &a.Title, &a.ManifestHash, &a.Model,
		&a.GroundingScope, &a.Status, &a.TotalItems, &a.CreatedAt, &a.UpdatedAt)
	if err == sql.ErrNoRows {
		return Assignment{}, false, nil
	}
	if err != nil {
		return Assignment{}, false, err
	}
	return a, true, nil
}

func (s *Store) ListAssignments() ([]Assignment, error) {
	rows, err := s.db.Query(
		`SELECT id, source_dir, title, manifest_hash, model,
                COALESCE(grounding_scope,''), status, total_items, created_at, updated_at
           FROM assignments ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Assignment
	for rows.Next() {
		var a Assignment
		if err := rows.Scan(&a.ID, &a.SourceDir, &a.Title, &a.ManifestHash, &a.Model,
			&a.GroundingScope, &a.Status, &a.TotalItems, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) ListAssignmentItems(assignmentID string) ([]AssignmentItem, error) {
	rows, err := s.db.Query(
		`SELECT id, assignment_id, seq, source_path, type, COALESCE(title,''),
                COALESCE(run_id,''), COALESCE(conversation_id,''), status,
                COALESCE(confidence,''), COALESCE(answer_json,''), COALESCE(flags_json,''),
                COALESCE(answer_path,''), COALESCE(error,''), created_at, updated_at
           FROM assignment_items WHERE assignment_id=? ORDER BY seq`, assignmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AssignmentItem
	for rows.Next() {
		var it AssignmentItem
		if err := rows.Scan(&it.ID, &it.AssignmentID, &it.Seq, &it.SourcePath, &it.Type,
			&it.Title, &it.RunID, &it.ConversationID, &it.Status, &it.Confidence,
			&it.AnswerJSON, &it.FlagsJSON, &it.AnswerPath, &it.Error,
			&it.CreatedAt, &it.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// GetAssignmentItem returns the item at (assignmentID, seq), or ok=false if none.
func (s *Store) GetAssignmentItem(assignmentID string, seq int) (AssignmentItem, bool, error) {
	var it AssignmentItem
	err := s.db.QueryRow(
		`SELECT id, assignment_id, seq, source_path, type, COALESCE(title,''),
                COALESCE(run_id,''), COALESCE(conversation_id,''), status,
                COALESCE(confidence,''), COALESCE(answer_json,''), COALESCE(flags_json,''),
                COALESCE(answer_path,''), COALESCE(error,''), created_at, updated_at
           FROM assignment_items WHERE assignment_id=? AND seq=?`, assignmentID, seq).Scan(
		&it.ID, &it.AssignmentID, &it.Seq, &it.SourcePath, &it.Type, &it.Title,
		&it.RunID, &it.ConversationID, &it.Status, &it.Confidence,
		&it.AnswerJSON, &it.FlagsJSON, &it.AnswerPath, &it.Error,
		&it.CreatedAt, &it.UpdatedAt)
	if err == sql.ErrNoRows {
		return AssignmentItem{}, false, nil
	}
	if err != nil {
		return AssignmentItem{}, false, err
	}
	return it, true, nil
}

// SetAssignmentScope stores the assignment's textbook scope as JSON in
// grounding_scope. An empty slice clears it (stored as NULL).
func (s *Store) SetAssignmentScope(asgID string, scopes []TextbookScope) error {
	var js string
	if len(scopes) > 0 {
		b, err := json.Marshal(scopes)
		if err != nil {
			return err
		}
		js = string(b)
	}
	_, err := s.db.Exec(`UPDATE assignments SET grounding_scope=?, updated_at=? WHERE id=?`,
		nullIfEmpty(js), time.Now().UnixMilli(), asgID)
	return err
}

// GetAssignmentScope returns the assignment's textbook scope (nil if none).
func (s *Store) GetAssignmentScope(asgID string) ([]TextbookScope, error) {
	var js string
	if err := s.db.QueryRow(
		`SELECT COALESCE(grounding_scope,'') FROM assignments WHERE id=?`, asgID).Scan(&js); err != nil {
		return nil, err
	}
	if js == "" {
		return nil, nil
	}
	var scopes []TextbookScope
	if err := json.Unmarshal([]byte(js), &scopes); err != nil {
		return nil, err
	}
	return scopes, nil
}

func (s *Store) SetConversationAssignment(convID, assignmentID string) error {
	_, err := s.db.Exec(`UPDATE conversations SET assignment_id=? WHERE id=?`,
		assignmentID, convID)
	return err
}

// nullIfEmpty maps "" to a SQL NULL so optional TEXT columns stay null rather
// than storing empty strings.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
