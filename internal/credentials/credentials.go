package credentials

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

const (
	DefaultEnvironmentPrefix = "TASKTUI_CREDENTIAL_"
	RedactedValue            = "[REDACTED]"
	DefaultKeyringService    = "tasktui"
)

var (
	ErrNotFound          = errors.New("credential not found")
	ErrInvalidReference  = errors.New("invalid credential reference")
	ErrInvalidCredential = errors.New("invalid credential")
)

// Secret is an in-memory credential value. Its ordinary formatting and
// serialization paths are deliberately redacted; Text is an explicit escape
// hatch for the provider client that must use the value.
type Secret struct {
	value string
}

func NewSecret(value string) Secret { return Secret{value: value} }

func (s Secret) Text() string  { return s.value }
func (s Secret) Value() string { return s.value }
func (s Secret) Empty() bool   { return s.value == "" }

func (s Secret) String() string   { return RedactedValue }
func (s Secret) GoString() string { return RedactedValue }

func (s Secret) MarshalJSON() ([]byte, error) {
	return json.Marshal(RedactedValue)
}

func (s Secret) MarshalText() ([]byte, error) {
	return []byte(RedactedValue), nil
}

func (s Secret) LogValue() slog.Value {
	return slog.StringValue(RedactedValue)
}

// Credential contains the non-secret identity and the resolved secret. It is
// safe to pass to slog.Any or fmt without exposing the secret.
type Credential struct {
	Reference string
	Username  string
	Secret    Secret
}

func NewCredential(reference, username, secret string) Credential {
	return Credential{
		Reference: reference,
		Username:  username,
		Secret:    NewSecret(secret),
	}
}

func (c Credential) String() string {
	if c.Reference == "" {
		return "credential"
	}
	return "credential(" + c.Reference + ")"
}

func (c Credential) GoString() string { return c.String() }

func (c Credential) LogValue() slog.Value {
	attrs := []slog.Attr{slog.String("reference", c.Reference)}
	if c.Username != "" {
		attrs = append(attrs, slog.String("username", c.Username))
	}
	attrs = append(attrs, slog.String("secret", RedactedValue))
	return slog.GroupValue(attrs...)
}

// NotFoundError is returned when a particular credential store has no value.
// Reference is retained for programmatic inspection but is not included in
// Error, so an accidental error log cannot expose a value used as a reference.
type NotFoundError struct {
	Reference string
	Store     string
}

func (e *NotFoundError) Error() string {
	if e == nil || e.Store == "" {
		return ErrNotFound.Error()
	}
	return "credential not found in " + e.Store
}

func (e *NotFoundError) Unwrap() error { return ErrNotFound }

func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound) || errors.Is(err, os.ErrNotExist)
}

// Store is the small interface implemented by keyring, encrypted, and other
// credential backends. Backend implementations must not log returned values.
type Store interface {
	Lookup(context.Context, string) (Credential, error)
}

type KeyringStore interface {
	Store
}

type EncryptedStore interface {
	Store
}

// KeyringBackend is an adapter boundary for an OS keyring implementation.
// The package deliberately does not choose a platform-specific dependency.
type KeyringBackend interface {
	Get(context.Context, string, string) (string, error)
}

type KeyringAdapter struct {
	backend KeyringBackend
	service string
}

func NewKeyringStore(backend KeyringBackend, service string) *KeyringAdapter {
	if strings.TrimSpace(service) == "" {
		service = DefaultKeyringService
	}
	return &KeyringAdapter{backend: backend, service: service}
}

func (s *KeyringAdapter) Lookup(ctx context.Context, reference string) (Credential, error) {
	if err := validateContextAndReference(ctx, reference); err != nil {
		return Credential{}, err
	}
	if s == nil || s.backend == nil {
		return Credential{}, &NotFoundError{Reference: reference, Store: "keyring"}
	}
	value, err := s.backend.Get(ctx, s.service, reference)
	if err != nil {
		if IsNotFound(err) {
			return Credential{}, &NotFoundError{Reference: reference, Store: "keyring"}
		}
		return Credential{}, fmt.Errorf("keyring lookup failed: %w", err)
	}
	return credentialFromValue(reference, value, "keyring")
}

// EncryptedBackend is an adapter boundary for an encrypted local store. The
// backend owns key management and decryption; this package never writes the
// returned value to disk.
type EncryptedBackend interface {
	Get(context.Context, string) (string, error)
}

type EncryptedAdapter struct {
	backend EncryptedBackend
}

func NewEncryptedStore(backend EncryptedBackend) *EncryptedAdapter {
	return &EncryptedAdapter{backend: backend}
}

func (s *EncryptedAdapter) Lookup(ctx context.Context, reference string) (Credential, error) {
	if err := validateContextAndReference(ctx, reference); err != nil {
		return Credential{}, err
	}
	if s == nil || s.backend == nil {
		return Credential{}, &NotFoundError{Reference: reference, Store: "encrypted store"}
	}
	value, err := s.backend.Get(ctx, reference)
	if err != nil {
		if IsNotFound(err) {
			return Credential{}, &NotFoundError{Reference: reference, Store: "encrypted store"}
		}
		return Credential{}, fmt.Errorf("encrypted credential lookup failed: %w", err)
	}
	return credentialFromValue(reference, value, "encrypted store")
}

type EnvironmentOptions struct {
	Prefix    string
	LookupEnv func(string) (string, bool)
}

// EnvironmentStore is intentionally opt-in. It maps a reference such as
// "clickup-work" to TASKTUI_CREDENTIAL_CLICKUP_WORK unless the reference uses
// the explicit env:NAME form.
type EnvironmentStore struct {
	Prefix    string
	LookupEnv func(string) (string, bool)
}

func NewEnvironmentStore(lookup ...func(string) (string, bool)) *EnvironmentStore {
	var lookupEnv func(string) (string, bool)
	if len(lookup) > 0 {
		lookupEnv = lookup[0]
	}
	return &EnvironmentStore{
		Prefix:    DefaultEnvironmentPrefix,
		LookupEnv: lookupEnv,
	}
}

func NewEnvironmentStoreWithOptions(options EnvironmentOptions) *EnvironmentStore {
	prefix := options.Prefix
	if prefix == "" {
		prefix = DefaultEnvironmentPrefix
	}
	return &EnvironmentStore{Prefix: prefix, LookupEnv: options.LookupEnv}
}

func (s *EnvironmentStore) Lookup(ctx context.Context, reference string) (Credential, error) {
	if err := validateContextAndReference(ctx, reference); err != nil {
		return Credential{}, err
	}
	if s == nil {
		return Credential{}, &NotFoundError{Reference: reference, Store: "environment"}
	}
	name, err := s.variableName(reference)
	if err != nil {
		return Credential{}, err
	}
	lookupEnv := s.LookupEnv
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	value, ok := lookupEnv(name)
	if !ok || value == "" {
		return Credential{}, &NotFoundError{Reference: reference, Store: "environment"}
	}
	return credentialFromValue(reference, value, "environment")
}

func (s *EnvironmentStore) variableName(reference string) (string, error) {
	if strings.HasPrefix(reference, "env:") {
		name := strings.TrimSpace(strings.TrimPrefix(reference, "env:"))
		if name == "" || strings.ContainsAny(name, " \t\r\n") {
			return "", fmt.Errorf("%w: invalid explicit environment variable reference", ErrInvalidReference)
		}
		return name, nil
	}
	normalized := normalizeReference(reference)
	if normalized == "" {
		return "", fmt.Errorf("%w: reference has no environment-safe name", ErrInvalidReference)
	}
	prefix := s.Prefix
	if prefix == "" {
		prefix = DefaultEnvironmentPrefix
	}
	return prefix + normalized, nil
}

type Resolver interface {
	Resolve(context.Context, string) (Credential, error)
}

type CredentialResolver = Resolver

type ResolverOptions struct {
	Keyring     KeyringStore
	Encrypted   EncryptedStore
	Environment Store
}

type ChainResolver struct {
	keyring     KeyringStore
	encrypted   EncryptedStore
	environment Store
}

// NewResolver creates a resolver in the preferred order: keyring, encrypted
// store, and finally the explicitly supplied environment store.
func NewResolver(options ResolverOptions) *ChainResolver {
	return &ChainResolver{
		keyring:     options.Keyring,
		encrypted:   options.Encrypted,
		environment: options.Environment,
	}
}

func NewChainResolver(options ResolverOptions) *ChainResolver { return NewResolver(options) }

// NewDefaultResolver enables the environment fallback explicitly while
// preserving the preferred keyring/encrypted ordering.
func NewDefaultResolver(keyring KeyringStore, encrypted EncryptedStore, lookup func(string) (string, bool)) *ChainResolver {
	return NewResolver(ResolverOptions{
		Keyring:     keyring,
		Encrypted:   encrypted,
		Environment: NewEnvironmentStore(lookup),
	})
}

func (r *ChainResolver) Resolve(ctx context.Context, reference string) (Credential, error) {
	if err := validateContextAndReference(ctx, reference); err != nil {
		return Credential{}, err
	}
	if r == nil {
		return Credential{}, &NotFoundError{Reference: reference, Store: "resolver"}
	}

	stores := []struct {
		name  string
		store Store
	}{
		{name: "keyring", store: r.keyring},
		{name: "encrypted store", store: r.encrypted},
	}
	for _, candidate := range stores {
		if candidate.store == nil {
			continue
		}
		credential, err := candidate.store.Lookup(ctx, reference)
		if err == nil {
			return validateCredential(reference, credential, candidate.name)
		}
		if IsNotFound(err) {
			continue
		}
		return Credential{}, fmt.Errorf("resolve credential from %s: %w", candidate.name, err)
	}

	if r.environment != nil {
		credential, err := r.environment.Lookup(ctx, reference)
		if err == nil {
			return validateCredential(reference, credential, "environment")
		}
		if !IsNotFound(err) {
			return Credential{}, fmt.Errorf("resolve credential from environment: %w", err)
		}
	}
	return Credential{}, &NotFoundError{Reference: reference, Store: "configured stores"}
}

func validateCredential(reference string, credential Credential, store string) (Credential, error) {
	if credential.Secret.Empty() {
		return Credential{}, fmt.Errorf("%w from %s", ErrInvalidCredential, store)
	}
	if credential.Reference == "" {
		credential.Reference = reference
	}
	return credential, nil
}

func credentialFromValue(reference, value, store string) (Credential, error) {
	if value == "" {
		return Credential{}, &NotFoundError{Reference: reference, Store: store}
	}
	return NewCredential(reference, "", value), nil
}

func validateContextAndReference(ctx context.Context, reference string) error {
	if ctx == nil {
		return errors.New("nil context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(reference) == "" || strings.ContainsAny(reference, "\r\n") {
		return fmt.Errorf("%w: reference must be non-empty and single-line", ErrInvalidReference)
	}
	return nil
}

func normalizeReference(reference string) string {
	var builder strings.Builder
	lastUnderscore := false
	for _, r := range strings.ToUpper(strings.TrimSpace(reference)) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			builder.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(builder.String(), "_")
}

type LookupFunc func(context.Context, string) (Credential, error)

func (f LookupFunc) Lookup(ctx context.Context, reference string) (Credential, error) {
	if f == nil {
		return Credential{}, &NotFoundError{Reference: reference, Store: "function store"}
	}
	return f(ctx, reference)
}
