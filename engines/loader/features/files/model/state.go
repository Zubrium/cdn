// Package model contains Loader file-feature state persisted through Core.
package model

import (
	"time"

	"github.com/Zubrium/cdn/engines/loader/features/files/pkg"
)

type UploadPart struct {
	Number int    `json:"number"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Stage  string `json:"stage"`
	State  string `json:"state"`
}

type Upload struct {
	ID                 string               `json:"id"`
	PrincipalID        string               `json:"principalId"`
	Input              pkg.BeginUploadInput `json:"input"`
	InputHash          string               `json:"inputHash"`
	IdempotencyKeyHash string               `json:"idempotencyKeyHash"`
	Mode               string               `json:"mode"`
	State              string               `json:"state"`
	ExpectedParts      int                  `json:"expectedParts"`
	FileID             string               `json:"fileId,omitempty"`
	CreatedAt          time.Time            `json:"createdAt"`
	ExpiresAt          time.Time            `json:"expiresAt"`
}

type IdempotencyRecord struct {
	InputHash string `json:"inputHash"`
	SessionID string `json:"sessionId"`
}

type CleanupRecord struct {
	UploadID      string `json:"uploadId"`
	PrincipalID   string `json:"principalId"`
	ExpectedParts int    `json:"expectedParts"`
	CommitAttempt bool   `json:"commitAttempt"`
	State         string `json:"state"`
}
