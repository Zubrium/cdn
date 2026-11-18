package domain

import "errors"

var (
	ErrInvalid     = errors.New("invalid Content data")
	ErrNotFound    = errors.New("Content data not found")
	ErrConflict    = errors.New("Content revision conflict")
	ErrUnavailable = errors.New("Content data unavailable")
	ErrTooLarge    = errors.New("Content data exceeds configured limits")
)
