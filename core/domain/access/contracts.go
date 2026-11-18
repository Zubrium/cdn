// Package access defines the Core's direct group-access model and errors.
package access

import "errors"

var ErrForbidden = errors.New("access forbidden")
var ErrUnauthorized = errors.New("unauthorized")

type Action string

const (
	Read   Action = "read"
	Write  Action = "write"
	Delete Action = "delete"
	Upload Action = "upload"

	Manage Action = "manage"
)

type Principal struct {
	ID                      string
	Name                    string
	Active                  bool
	CanManageAdministrators bool
}

type Resource struct {
	ID     string
	Ready  bool
	Groups []string
}
