// Package files implements owner-scoped, resumable binary file storage.
package files

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"time"
)

var (
	ErrNotFound  = errors.New("file not found")
	ErrForbidden = errors.New("file access denied")
	ErrConflict  = errors.New("file state changed")
	ErrInvalid   = errors.New("invalid file request")
	ErrExpired   = errors.New("upload expired")
	ErrQuota     = errors.New("file quota exceeded")
	ErrInUse     = errors.New("file has known references; confirmation is required")
)

type Owner struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	Kind string    `json:"kind"`
}
type File struct {
	ID          uuid.UUID `json:"id"`
	OwnerID     uuid.UUID `json:"ownerId"`
	Name        string    `json:"name"`
	Size        int64     `json:"size"`
	ContentType string    `json:"contentType"`
	SHA256      string    `json:"sha256"`
	Visibility  string    `json:"visibility"`
	CreatedAt   time.Time `json:"createdAt"`
	ObjectKey   string    `json:"-"`
}
type UploadInput struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
	Visibility string `json:"visibility"`
}
type Part struct {
	Number    int    `json:"number"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	Ready     bool   `json:"ready"`
	ObjectKey string `json:"-"`
}
type Upload struct {
	ID         uuid.UUID `json:"id"`
	FileID     uuid.UUID `json:"fileId"`
	OwnerID    uuid.UUID `json:"-"`
	Name       string    `json:"name"`
	Size       int64     `json:"size"`
	SHA256     string    `json:"sha256"`
	Visibility string    `json:"visibility"`
	PartSize   int64     `json:"partSize"`
	State      string    `json:"state"`
	ExpiresAt  time.Time `json:"expiresAt"`
	Parts      []Part    `json:"parts"`
}
type Reference struct {
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	Description string `json:"description"`
}
type ReferenceManager interface {
	References(context.Context, uuid.UUID) ([]Reference, error)
	ReferencesTx(context.Context, pgx.Tx, uuid.UUID) ([]Reference, error)
	RemoveReferences(context.Context, pgx.Tx, uuid.UUID) error
}
