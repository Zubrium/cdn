package model

import "time"

// Submission contains accepted answer data. Answer values are immutable; only
// State may change through the administrative API.
type Submission struct {
	ID        string
	FormID    string
	State     string
	Values    map[string]any
	CreatedAt time.Time
}
