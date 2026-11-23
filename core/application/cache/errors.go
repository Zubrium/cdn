package cache

import "errors"

type transientError struct{ err error }

func (e *transientError) Error() string { return e.err.Error() }
func (e *transientError) Unwrap() error { return e.err }

// Transient marks a source failure that may be retried within the configured
// bound. Authorization, validation, not-found and revision errors are never
// wrapped with this marker.
func Transient(err error) error {
	if err == nil {
		return nil
	}
	return &transientError{err: err}
}

func isTransient(err error) bool {
	var target *transientError
	return errors.As(err, &target)
}
