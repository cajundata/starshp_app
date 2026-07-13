package store

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Conversation struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`
	PinnedModel string `json:"pinnedModel"`
}

type Message struct {
	ID                string `json:"id"`
	ConvID            string `json:"conversationId"`
	Role              string `json:"role"`
	Content           string `json:"content"`
	Model             string `json:"model"`
	CreatedAt         int64  `json:"createdAt"`
	RAGContext        string `json:"ragContext"`
	RAGSources        string `json:"ragSources"`
	InputTokens       *int   `json:"inputTokens,omitempty"`
	OutputTokens      *int   `json:"outputTokens,omitempty"`
	CachedInputTokens *int   `json:"cachedInputTokens,omitempty"`
}

type TextbookScope struct {
	Name     string `json:"name"`
	Chapters []int  `json:"chapters"`
}

func (s *Store) CreateConversation(title string) (Conversation, error) {
	now := time.Now().Unix()
	c := Conversation{ID: uuid.NewString(), Title: title, CreatedAt: now, UpdatedAt: now}
	_, err := s.db.Exec(`INSERT INTO conversations(id,title,created_at,updated_at) VALUES(?,?,?,?)`,
		c.ID, c.Title, c.CreatedAt, c.UpdatedAt)
	return c, err
}

func (s *Store) ListConversations() ([]Conversation, error) {
	rows, err := s.db.Query(`SELECT id,title,created_at,updated_at,COALESCE(pinned_model,'') FROM conversations ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Conversation
	for rows.Next() {
		var c Conversation
		if err := rows.Scan(&c.ID, &c.Title, &c.CreatedAt, &c.UpdatedAt, &c.PinnedModel); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) DeleteConversation(id string) error {
	_, err := s.db.Exec(`DELETE FROM conversations WHERE id=?`, id)
	return err
}

func (s *Store) SetConversationTitle(id, title string) error {
	_, err := s.db.Exec(`UPDATE conversations SET title=?, updated_at=? WHERE id=?`,
		title, time.Now().Unix(), id)
	return err
}

func (s *Store) SetConversationMeta(id, pinnedModel string) error {
	_, err := s.db.Exec(`UPDATE conversations SET pinned_model=?,updated_at=? WHERE id=?`,
		pinnedModel, time.Now().Unix(), id)
	return err
}

// Message is the legacy chat message shape. The messages table is retired by
// the tool-calling migration (data lives in conversation_events now); the
// struct is retained only for the deprecated appapi.ListMessages shim's
// signature during frontend rollout.
//
// GetRetrievalMode returns the per-conversation retrieval policy
// (defaults to 'auto_grounded_default' for rows created before the column).
func (s *Store) GetRetrievalMode(convID string) (string, error) {
	var mode string
	err := s.db.QueryRow(`SELECT retrieval_mode FROM conversations WHERE id = ?`, convID).Scan(&mode)
	return mode, err
}

// SetRetrievalMode updates the per-conversation retrieval policy.
func (s *Store) SetRetrievalMode(convID, mode string) error {
	_, err := s.db.Exec(`UPDATE conversations SET retrieval_mode = ? WHERE id = ?`, mode, convID)
	return err
}

func (s *Store) SetConversationTextbooks(convID string, scopes []TextbookScope) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM conversation_textbooks WHERE conversation_id=?`, convID); err != nil {
		return err
	}
	for _, sc := range scopes {
		var chJSON string
		if sc.Chapters != nil {
			b, _ := json.Marshal(sc.Chapters)
			chJSON = string(b)
		}
		if _, err := tx.Exec(`INSERT INTO conversation_textbooks(conversation_id,textbook_name,chapter_nums) VALUES(?,?,?)`,
			convID, sc.Name, chJSON); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) GetConversationTextbooks(convID string) ([]TextbookScope, error) {
	rows, err := s.db.Query(`SELECT textbook_name,COALESCE(chapter_nums,'') FROM conversation_textbooks WHERE conversation_id=?`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TextbookScope
	for rows.Next() {
		var sc TextbookScope
		var chJSON string
		if err := rows.Scan(&sc.Name, &chJSON); err != nil {
			return nil, err
		}
		if chJSON != "" {
			json.Unmarshal([]byte(chJSON), &sc.Chapters)
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}
