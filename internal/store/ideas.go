package store

import "time"

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
