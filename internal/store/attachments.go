package store

import (
	"context"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5"
)

var ErrNoAttachment = errors.New("attachment not found")

type Attachment struct {
	ID        string
	UserID    string // the uploader
	Name      string
	MediaType string
	Size      int64
	Data      []byte // only set by Attachments
}

func (s *Store) CreateAttachment(ctx context.Context, a Attachment) error {
	_, err := s.db.Exec(ctx, `INSERT INTO attachments (id, user_id, name, media_type, data) VALUES ($1, $2, $3, $4, $5)`,
		a.ID, a.UserID, a.Name, a.MediaType, a.Data)
	return err
}

// Attachments returns the attachments with the given ids in that order, with
// their data if withData. A missing id is ErrNoAttachment.
func (s *Store) Attachments(ctx context.Context, ids []string, withData bool) ([]Attachment, error) {
	rows, err := s.db.Query(ctx, `SELECT id, user_id, name, media_type, octet_length(data),
		CASE WHEN $2 THEN data ELSE ''::bytea END FROM attachments WHERE id = ANY($1)`, ids, withData)
	if err != nil {
		return nil, err
	}
	byID, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Attachment, error) {
		var a Attachment
		return a, r.Scan(&a.ID, &a.UserID, &a.Name, &a.MediaType, &a.Size, &a.Data)
	})
	if err != nil {
		return nil, err
	}
	out := make([]Attachment, len(ids))
	for i, id := range ids {
		j := slices.IndexFunc(byID, func(a Attachment) bool { return a.ID == id })
		if j < 0 {
			return nil, ErrNoAttachment
		}
		out[i] = byID[j]
	}
	return out, nil
}
