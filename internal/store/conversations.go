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
	PresetID    string `json:"presetId"`
	PinnedModel string `json:"pinnedModel"`
}

type Message struct {
	ID         string `json:"id"`
	ConvID     string `json:"conversationId"`
	Role       string `json:"role"`
	Content    string `json:"content"`
	Model      string `json:"model"`
	CreatedAt  int64  `json:"createdAt"`
	RAGContext string `json:"ragContext"`
	RAGSources string `json:"ragSources"`
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
	rows, err := s.db.Query(`SELECT id,title,created_at,updated_at,COALESCE(preset_id,''),COALESCE(pinned_model,'') FROM conversations ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Conversation
	for rows.Next() {
		var c Conversation
		if err := rows.Scan(&c.ID, &c.Title, &c.CreatedAt, &c.UpdatedAt, &c.PresetID, &c.PinnedModel); err != nil {
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

func (s *Store) SetConversationMeta(id, presetID, pinnedModel string) error {
	_, err := s.db.Exec(`UPDATE conversations SET preset_id=?,pinned_model=?,updated_at=? WHERE id=?`,
		presetID, pinnedModel, time.Now().Unix(), id)
	return err
}

func (s *Store) AddMessage(convID, role, content, model, ragContext, ragSources string) (Message, error) {
	m := Message{ID: uuid.NewString(), ConvID: convID, Role: role, Content: content,
		Model: model, CreatedAt: time.Now().Unix(), RAGContext: ragContext, RAGSources: ragSources}
	_, err := s.db.Exec(`INSERT INTO messages(id,conversation_id,role,content,model,created_at,rag_context,rag_sources) VALUES(?,?,?,?,?,?,?,?)`,
		m.ID, m.ConvID, m.Role, m.Content, m.Model, m.CreatedAt, m.RAGContext, m.RAGSources)
	if err == nil {
		s.db.Exec(`UPDATE conversations SET updated_at=? WHERE id=?`, m.CreatedAt, convID)
	}
	return m, err
}

func (s *Store) ListMessages(convID string) ([]Message, error) {
	rows, err := s.db.Query(`SELECT id,conversation_id,role,content,COALESCE(model,''),created_at,COALESCE(rag_context,''),COALESCE(rag_sources,'') FROM messages WHERE conversation_id=? ORDER BY created_at`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.ConvID, &m.Role, &m.Content, &m.Model, &m.CreatedAt, &m.RAGContext, &m.RAGSources); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
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
