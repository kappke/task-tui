package clickup

import (
	"context"
	"fmt"
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
		return "", fmt.Errorf("%w: %s is not set", ErrMissingTokenSource, TokenEnvVar)
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
