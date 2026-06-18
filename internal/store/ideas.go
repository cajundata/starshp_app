package store

import (
	"time"

	"github.com/google/uuid"
)

type Idea struct {
	ID            string
	Title         string
	Summary       string
	Pathway       string
	Status        string
	KillReason    string
	FinancialFlag bool
	Source        string
	CreatedAt     int64
	UpdatedAt     int64
}

type StatusChange struct {
	ID         string
	IdeaID     string
	FromStatus string
	ToStatus   string
	Reason     string
	CreatedAt  int64
}

type KillCriterion struct {
	ID         string
	IdeaID     string
	ReviewID   string
	Metric     string
	Threshold  string
	ReviewDate int64
	OnMiss     string
	Status     string
	Notes      string
	CreatedAt  int64
	UpdatedAt  int64
}

// DueReview is one overdue/due kill criterion joined to its parent idea, as
// returned by the launch sweep.
type DueReview struct {
	CriterionID string
	IdeaID      string
	IdeaTitle   string
	IdeaStatus  string
	Metric      string
	Threshold   string
	ReviewDate  int64
	OnMiss      string
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Store) CreateIdea(i Idea) error {
	now := time.Now().UnixMilli()
	_, err := s.db.Exec(
		`INSERT INTO ideas
		    (id, title, summary, pathway, status, kill_reason,
		     financial_flag, source, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		i.ID, i.Title, i.Summary, nullIfEmpty(i.Pathway), i.Status,
		nullIfEmpty(i.KillReason), boolToInt(i.FinancialFlag),
		defaultStr(i.Source, "manual"), now, now)
	return err
}

// UpdateIdea overwrites the editable fields (title, summary, pathway,
// financial_flag). Status is changed only through SetIdeaStatus.
func (s *Store) UpdateIdea(i Idea) error {
	_, err := s.db.Exec(
		`UPDATE ideas SET title=?, summary=?, pathway=?, financial_flag=?, updated_at=?
		  WHERE id=?`,
		i.Title, i.Summary, nullIfEmpty(i.Pathway), boolToInt(i.FinancialFlag),
		time.Now().UnixMilli(), i.ID)
	return err
}

func (s *Store) GetIdea(id string) (Idea, error) {
	return scanIdea(s.db.QueryRow(
		`SELECT id, title, summary, COALESCE(pathway,''), status,
		        COALESCE(kill_reason,''), financial_flag, source, created_at, updated_at
		   FROM ideas WHERE id=?`, id))
}

func (s *Store) ListIdeas() ([]Idea, error) {
	rows, err := s.db.Query(
		`SELECT id, title, summary, COALESCE(pathway,''), status,
		        COALESCE(kill_reason,''), financial_flag, source, created_at, updated_at
		   FROM ideas ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Idea
	for rows.Next() {
		i, err := scanIdea(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *Store) DeleteIdea(id string) error {
	_, err := s.db.Exec(`DELETE FROM ideas WHERE id=?`, id)
	return err
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanIdea(r rowScanner) (Idea, error) {
	var i Idea
	var fin int
	err := r.Scan(&i.ID, &i.Title, &i.Summary, &i.Pathway, &i.Status,
		&i.KillReason, &fin, &i.Source, &i.CreatedAt, &i.UpdatedAt)
	i.FinancialFlag = fin != 0
	return i, err
}

// defaultStr returns def when s is empty.
func defaultStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// SetIdeaStatus updates the idea's status and appends a status-history row in a
// single transaction. For terminal statuses (killed, parked) the reason is also
// stored on the idea's kill_reason column. It is mechanical: legality of the
// transition is validated by the pipeline package before this is called.
func (s *Store) SetIdeaStatus(id, toStatus, reason string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var from string
	if err := tx.QueryRow(`SELECT status FROM ideas WHERE id=?`, id).Scan(&from); err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	if toStatus == "killed" || toStatus == "parked" {
		if _, err := tx.Exec(
			`UPDATE ideas SET status=?, kill_reason=?, updated_at=? WHERE id=?`,
			toStatus, reason, now, id); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(
			`UPDATE ideas SET status=?, updated_at=? WHERE id=?`,
			toStatus, now, id); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(
		`INSERT INTO idea_status_history (id, idea_id, from_status, to_status, reason, created_at)
		 VALUES (?,?,?,?,?,?)`,
		uuid.NewString(), id, from, toStatus, reason, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListStatusHistory(ideaID string) ([]StatusChange, error) {
	rows, err := s.db.Query(
		`SELECT id, idea_id, COALESCE(from_status,''), to_status, reason, created_at
		   FROM idea_status_history WHERE idea_id=? ORDER BY created_at ASC, rowid ASC`, ideaID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StatusChange
	for rows.Next() {
		var c StatusChange
		if err := rows.Scan(&c.ID, &c.IdeaID, &c.FromStatus, &c.ToStatus,
			&c.Reason, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) AddKillCriterion(k KillCriterion) error {
	now := time.Now().UnixMilli()
	_, err := s.db.Exec(
		`INSERT INTO kill_criteria
		    (id, idea_id, review_id, metric, threshold, review_date,
		     on_miss, status, notes, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		k.ID, k.IdeaID, nullIfEmpty(k.ReviewID), k.Metric, k.Threshold, k.ReviewDate,
		k.OnMiss, defaultStr(k.Status, "pending"), k.Notes, now, now)
	return err
}

func (s *Store) UpdateKillCriterion(k KillCriterion) error {
	_, err := s.db.Exec(
		`UPDATE kill_criteria
		    SET metric=?, threshold=?, review_date=?, on_miss=?, status=?, notes=?, updated_at=?
		  WHERE id=?`,
		k.Metric, k.Threshold, k.ReviewDate, k.OnMiss, k.Status, k.Notes,
		time.Now().UnixMilli(), k.ID)
	return err
}

func (s *Store) DeleteKillCriterion(id string) error {
	_, err := s.db.Exec(`DELETE FROM kill_criteria WHERE id=?`, id)
	return err
}

func (s *Store) ListKillCriteria(ideaID string) ([]KillCriterion, error) {
	rows, err := s.db.Query(
		`SELECT id, idea_id, COALESCE(review_id,''), metric, threshold, review_date,
		        on_miss, status, notes, created_at, updated_at
		   FROM kill_criteria WHERE idea_id=? ORDER BY review_date ASC`, ideaID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KillCriterion
	for rows.Next() {
		var k KillCriterion
		if err := rows.Scan(&k.ID, &k.IdeaID, &k.ReviewID, &k.Metric, &k.Threshold,
			&k.ReviewDate, &k.OnMiss, &k.Status, &k.Notes, &k.CreatedAt, &k.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// ListDueKillCriteria returns pending kill criteria whose review_date is at or
// before asOf, joined to the parent idea, oldest review date first. Criteria on
// killed ideas are excluded — a killed idea is permanent, so its reviews should
// not resurface. Parked ideas are intentionally included: their reviews are the
// reminder to revisit them.
func (s *Store) ListDueKillCriteria(asOf int64) ([]DueReview, error) {
	rows, err := s.db.Query(
		`SELECT k.id, k.idea_id, i.title, i.status, k.metric, k.threshold,
		        k.review_date, k.on_miss
		   FROM kill_criteria k JOIN ideas i ON i.id = k.idea_id
		  WHERE k.status='pending' AND k.review_date <= ? AND i.status != 'killed'
		  ORDER BY k.review_date ASC`, asOf)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DueReview
	for rows.Next() {
		var d DueReview
		if err := rows.Scan(&d.CriterionID, &d.IdeaID, &d.IdeaTitle, &d.IdeaStatus,
			&d.Metric, &d.Threshold, &d.ReviewDate, &d.OnMiss); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
