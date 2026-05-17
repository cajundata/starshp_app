package store

import (
	"time"

	"github.com/google/uuid"
)

type Preset struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	SystemPrompt string `json:"systemPrompt"`
	CreatedAt    int64  `json:"createdAt"`
	UpdatedAt    int64  `json:"updatedAt"`
}

func (s *Store) CreatePreset(name, systemPrompt string) (Preset, error) {
	now := time.Now().Unix()
	p := Preset{ID: uuid.NewString(), Name: name, SystemPrompt: systemPrompt, CreatedAt: now, UpdatedAt: now}
	_, err := s.db.Exec(`INSERT INTO presets(id,name,system_prompt,created_at,updated_at) VALUES(?,?,?,?,?)`,
		p.ID, p.Name, p.SystemPrompt, p.CreatedAt, p.UpdatedAt)
	return p, err
}

func (s *Store) ListPresets() ([]Preset, error) {
	rows, err := s.db.Query(`SELECT id,name,system_prompt,created_at,updated_at FROM presets ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Preset
	for rows.Next() {
		var p Preset
		if err := rows.Scan(&p.ID, &p.Name, &p.SystemPrompt, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) UpdatePreset(id, name, systemPrompt string) error {
	_, err := s.db.Exec(`UPDATE presets SET name=?,system_prompt=?,updated_at=? WHERE id=?`,
		name, systemPrompt, time.Now().Unix(), id)
	return err
}

func (s *Store) DeletePreset(id string) error {
	_, err := s.db.Exec(`DELETE FROM presets WHERE id=?`, id)
	return err
}
