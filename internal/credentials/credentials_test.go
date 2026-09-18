package credentials

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestResolverFallsBackToEnvironmentExplicitly(t *testing.T) {
	const reference = "clickup-work"
	const secret = "environment-secret-value"

	keyring := LookupFunc(func(context.Context, string) (Credential, error) {
		return Credential{}, &NotFoundError{Store: "keyring"}
	})
	encrypted := LookupFunc(func(context.Context, string) (Credential, error) {
		return Credential{}, &NotFoundError{Store: "encrypted store"}
	})
	lookup := func(name string) (string, bool) {
		if name == "TASKTUI_CREDENTIAL_CLICKUP_WORK" {
			return secret, true
		}
		return "", false
	}

	resolver := NewDefaultResolver(keyring, encrypted, lookup)
	credential, err := resolver.Resolve(context.Background(), reference)
	if err != nil {
		t.Fatalf("resolve environment credential: %v", err)
	}
	if credential.Secret.Text() != secret {
		t.Fatal("environment credential value was not resolved")
	}
}

func TestResolverPrefersEncryptedStoreOverEnvironment(t *testing.T) {
	const reference = "clickup-work"
	const encryptedSecret = "encrypted-secret-value"

	keyring := LookupFunc(func(context.Context, string) (Credential, error) {
		return Credential{}, ErrNotFound
	})
	encrypted := LookupFunc(func(context.Context, string) (Credential, error) {
		return NewCredential(reference, "", encryptedSecret), nil
	})
	environment := NewEnvironmentStore(func(string) (string, bool) {
		return "environment-value", true
	})

	resolver := NewResolver(ResolverOptions{
		Keyring:     keyring,
		Encrypted:   encrypted,
		Environment: environment,
	})
	credential, err := resolver.Resolve(context.Background(), reference)
	if err != nil {
		t.Fatal(err)
	}
	if credential.Secret.Text() != encryptedSecret {
		t.Fatal("resolver did not prefer encrypted store")
	}
}

func TestResolverNotFoundIsTyped(t *testing.T) {
	resolver := NewResolver(ResolverOptions{})
	_, err := resolver.Resolve(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatal("resolver did not return ErrNotFound")
	}
	var notFound *NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatal("resolver did not return a typed not-found error")
	}
}

func TestSecretRepresentationsAreRedacted(t *testing.T) {
	const secret = "must-never-be-printed"
	credential := NewCredential("work", "user", secret)

	if strings.Contains(fmt.Sprint(credential.Secret), secret) || strings.Contains(fmt.Sprintf("%+v", credential), secret) {
		t.Fatal("credential formatting exposed the secret")
	}
	data, err := json.Marshal(credential)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) || !strings.Contains(string(data), RedactedValue) {
		t.Fatal("credential JSON representation was not redacted")
	}
}
