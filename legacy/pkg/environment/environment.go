// Package environment carries the configured branch identifier on requests.
package environment

import (
	"context"
	"fmt"
	"github.com/google/uuid"
)

type Environment string

func Parse(raw string) (Environment, error) {
	branchID, err := uuid.Parse(raw)
	if err == nil {
		return Environment(branchID.String()), nil
	}
	return "", fmt.Errorf("branch ID must be a UUID")
}

func (e Environment) Valid() bool {
	_, err := uuid.Parse(string(e))
	return err == nil
}

func (e Environment) String() string { return string(e) }

type contextKey struct{}

func WithContext(ctx context.Context, env Environment) context.Context {
	return context.WithValue(ctx, contextKey{}, env)
}

func FromContext(ctx context.Context) Environment {
	if env, ok := ctx.Value(contextKey{}).(Environment); ok && env.Valid() {
		return env
	}
	return ""
}
