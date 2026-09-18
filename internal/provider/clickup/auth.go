package clickup

import (
	"context"
	"errors"
	"os"
)

const TokenEnvVar = "CLICKUP_API_TOKEN"

// TokenSource is the context-aware form accepted by ClientConfig.
type TokenSource interface {
	Token(context.Context) (string, error)
}

// TokenSourceFunc adapts a function to TokenSource.
type TokenSourceFunc func(context.Context) (string, error)

func (f TokenSourceFunc) Token(ctx context.Context) (string, error) {
	return f(ctx)
}

// TokenFromEnv lazily reads the ClickUp token from the environment.
func TokenFromEnv() (string, error) {
	token := os.Getenv(TokenEnvVar)
	if token == "" {
		return "", errors.New("CLICKUP_API_TOKEN is not set")
	}
	return token, nil
}

// EnvTokenSource is a context-aware environment token source.
func EnvTokenSource(ctx context.Context) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}
	return TokenFromEnv()
}

type staticTokenSource string

func (s staticTokenSource) Token(context.Context) (string, error) {
	if s == "" {
		return "", ErrMissingTokenSource
	}
	return string(s), nil
}
