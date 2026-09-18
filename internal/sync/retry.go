package sync

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/kappke/task-tui/internal/domain"
)

// FailureKind is the durable classification of a provider or queue failure.
type FailureKind string

const (
	FailureRetryable   FailureKind = "retryable"
	FailureRateLimited FailureKind = "rate_limited"
	FailureTerminal    FailureKind = "terminal"
	FailureCanceled    FailureKind = "canceled"
)

// RetryDecisionKind describes what the worker should do after a failure.
type RetryDecisionKind string

const (
	DecisionNone        RetryDecisionKind = "none"
	DecisionRetry       RetryDecisionKind = "retry"
	DecisionRateLimited RetryDecisionKind = "rate_limited"
	DecisionTerminal    RetryDecisionKind = "terminal"
	DecisionCanceled    RetryDecisionKind = "canceled"
)

// RetryDecision is typed so callers do not need to inspect provider error
// strings to distinguish retry, rate-limit, terminal, and cancellation paths.
type RetryDecision struct {
	Kind       RetryDecisionKind
	Failure    FailureKind
	Delay      time.Duration
	Attempts   int
	RetryAfter time.Duration
	Err        error
}

// ShouldRetry reports whether the decision schedules another provider call.
func (d RetryDecision) ShouldRetry() bool {
	return d.Kind == DecisionRetry || d.Kind == DecisionRateLimited
}

// Failure is the value persisted by Queue.Fail. RetryAt is nil for terminal
// failures and points to the earliest next attempt for retryable failures.
type Failure struct {
	Kind     FailureKind
	Err      error
	Attempts int
	RetryAt  *time.Time
}

// Error returns the underlying failure text for logging and adapters.
func (f Failure) Error() string {
	if f.Err == nil {
		return string(f.Kind)
	}
	return f.Err.Error()
}

// RetryableError marks a provider error as safe for sync-level retry.
type RetryableError struct {
	Err error
}

func (e RetryableError) Error() string {
	if e.Err == nil {
		return string(FailureRetryable)
	}
	return e.Err.Error()
}

func (e RetryableError) Unwrap() error {
	return e.Err
}

// Retryable wraps an error as a retryable provider failure.
func Retryable(err error) error {
	return RetryableError{Err: err}
}

// TerminalError marks a provider error that must be persisted without retry.
type TerminalError struct {
	Err error
}

func (e TerminalError) Error() string {
	if e.Err == nil {
		return string(FailureTerminal)
	}
	return e.Err.Error()
}

func (e TerminalError) Unwrap() error {
	return e.Err
}

// Terminal wraps an error as a terminal provider failure.
func Terminal(err error) error {
	return TerminalError{Err: err}
}

// RateLimitError marks a provider response that must wait at least
// RetryAfter. The sync policy may choose a longer delay, but never a shorter
// one.
type RateLimitError struct {
	Err        error
	RetryAfter time.Duration
}

func (e RateLimitError) Error() string {
	if e.Err == nil {
		return string(FailureRateLimited)
	}
	return e.Err.Error()
}

func (e RateLimitError) Unwrap() error {
	return e.Err
}

// RateLimited wraps an error with a provider-supplied minimum delay.
func RateLimited(err error, after time.Duration) error {
	return RateLimitError{Err: err, RetryAfter: after}
}

// FailureError is useful for adapters that already have a typed failure
// classification and optionally a rate-limit duration.
type FailureError struct {
	Kind       FailureKind
	Err        error
	RetryAfter time.Duration
}

func (e FailureError) Error() string {
	if e.Err == nil {
		return string(e.Kind)
	}
	return e.Err.Error()
}

func (e FailureError) Unwrap() error {
	return e.Err
}

// Jitter changes a calculated delay. A nil Jitter is deterministic and keeps
// the documented schedule exact, which is also useful in tests.
type Jitter func(time.Duration, int) time.Duration

// RetryPolicy implements the documented 1s/5s/15s/30s/1m/5m schedule. After
// the final entry, retryable failures continue at the bounded 5m delay unless
// MaxAttempts is set. Rate-limit failures use the greater of this schedule and
// the provider-supplied RetryAfter value.
type RetryPolicy struct {
	Schedule    []time.Duration
	MaxAttempts int
	Jitter      Jitter
}

var defaultRetrySchedule = [...]time.Duration{
	time.Second,
	5 * time.Second,
	15 * time.Second,
	30 * time.Second,
	time.Minute,
	5 * time.Minute,
}

// DefaultRetrySchedule returns a copy of the documented retry schedule.
func DefaultRetrySchedule() []time.Duration {
	return append([]time.Duration(nil), defaultRetrySchedule[:]...)
}

// DefaultRetryPolicy returns deterministic sync backoff with no attempt cap.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{Schedule: DefaultRetrySchedule()}
}

// RetryDelay is the default deterministic delay for a one-based attempt
// count. It is kept as a small convenience for callers that do not need a
// complete RetryPolicy value.
func RetryDelay(attempt int) time.Duration {
	return DefaultRetryPolicy().Delay(attempt)
}

// NewRetryPolicy creates a policy from a schedule. An empty schedule selects
// the documented default.
func NewRetryPolicy(schedule []time.Duration) RetryPolicy {
	if len(schedule) == 0 {
		return DefaultRetryPolicy()
	}
	return RetryPolicy{Schedule: append([]time.Duration(nil), schedule...)}
}

// Delay returns the policy delay for a one-based attempt count. Values after
// the six documented entries remain bounded at five minutes by default.
func (p RetryPolicy) Delay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	schedule := p.Schedule
	if len(schedule) == 0 {
		schedule = defaultRetrySchedule[:]
	}
	index := attempt - 1
	if index >= len(schedule) {
		index = len(schedule) - 1
	}
	delay := schedule[index]
	if delay < 0 {
		delay = 0
	}
	if p.Jitter != nil {
		delay = p.Jitter(delay, attempt)
		if delay < 0 {
			delay = 0
		}
	}
	return delay
}

// Decide classifies err and calculates the next action for attempts, where
// attempts is the count persisted before the provider call that failed.
func (p RetryPolicy) Decide(err error, attempts int) RetryDecision {
	if err == nil {
		return RetryDecision{Kind: DecisionNone, Attempts: attempts}
	}
	if attempts < 1 {
		attempts = 1
	}

	failure := ClassifyFailure(err)
	decision := RetryDecision{
		Failure:  failure,
		Attempts: attempts,
		Err:      err,
	}

	switch failure {
	case FailureCanceled:
		decision.Kind = DecisionCanceled
		return decision
	case FailureTerminal:
		decision.Kind = DecisionTerminal
		return decision
	}

	if p.MaxAttempts > 0 && attempts >= p.MaxAttempts {
		decision.Kind = DecisionTerminal
		decision.Failure = FailureTerminal
		return decision
	}

	decision.Delay = p.Delay(attempts)
	if failure == FailureRateLimited {
		decision.Kind = DecisionRateLimited
		decision.RetryAfter = rateLimitAfter(err)
		if decision.RetryAfter > decision.Delay {
			decision.Delay = decision.RetryAfter
		}
		return decision
	}
	decision.Kind = DecisionRetry
	return decision
}

// ClassifyFailure converts provider error contracts into a sync failure kind.
// Unknown provider errors are retryable: transient network errors are normal
// in an offline-first application, while permanent failures should be wrapped
// with Terminal or FailureError by the provider boundary.
func ClassifyFailure(err error) FailureKind {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return FailureCanceled
	}
	if errors.Is(err, ErrProviderMismatch) || errors.Is(err, ErrUnsupported) || errors.Is(err, ErrInvalidPayload) {
		return FailureTerminal
	}
	if errors.Is(err, domain.ErrProviderMismatch) || errors.Is(err, domain.ErrUnsupported) ||
		errors.Is(err, domain.ErrInvalidID) || errors.Is(err, domain.ErrInvalidEnum) ||
		errors.Is(err, domain.ErrInvalidParent) || errors.Is(err, domain.ErrAlreadyExists) ||
		errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrConflict) {
		return FailureTerminal
	}

	var classifiedPointer *FailureError
	if errors.As(err, &classifiedPointer) && classifiedPointer != nil {
		return normalizeFailureKind(classifiedPointer.Kind)
	}
	var classifiedValue FailureError
	if errors.As(err, &classifiedValue) {
		return normalizeFailureKind(classifiedValue.Kind)
	}
	var rateLimitPointer *RateLimitError
	if errors.As(err, &rateLimitPointer) && rateLimitPointer != nil {
		return FailureRateLimited
	}
	var rateLimitValue RateLimitError
	if errors.As(err, &rateLimitValue) {
		return FailureRateLimited
	}
	var terminalPointer *TerminalError
	if errors.As(err, &terminalPointer) && terminalPointer != nil {
		return FailureTerminal
	}
	var terminalValue TerminalError
	if errors.As(err, &terminalValue) {
		return FailureTerminal
	}
	var retryablePointer *RetryableError
	if errors.As(err, &retryablePointer) && retryablePointer != nil {
		return FailureRetryable
	}
	var retryableValue RetryableError
	if errors.As(err, &retryableValue) {
		return FailureRetryable
	}

	var terminalMarker interface{ Terminal() bool }
	if errors.As(err, &terminalMarker) && terminalMarker.Terminal() {
		return FailureTerminal
	}
	var permanentMarker interface{ Permanent() bool }
	if errors.As(err, &permanentMarker) && permanentMarker.Permanent() {
		return FailureTerminal
	}
	var rateMarker interface{ RateLimit() bool }
	if errors.As(err, &rateMarker) && rateMarker.RateLimit() {
		return FailureRateLimited
	}
	var afterMarker interface{ RetryAfterDuration() time.Duration }
	if errors.As(err, &afterMarker) {
		return FailureRateLimited
	}
	var retryMarker interface{ Retryable() bool }
	if errors.As(err, &retryMarker) && retryMarker.Retryable() {
		return FailureRetryable
	}
	var temporaryMarker interface{ Temporary() bool }
	if errors.As(err, &temporaryMarker) && temporaryMarker.Temporary() {
		return FailureRetryable
	}
	if rateLimitAfter(err) > 0 {
		return FailureRateLimited
	}
	return FailureRetryable
}

func normalizeFailureKind(kind FailureKind) FailureKind {
	switch kind {
	case FailureRateLimited, FailureTerminal, FailureCanceled:
		return kind
	default:
		return FailureRetryable
	}
}

func rateLimitAfter(err error) time.Duration {
	var classifiedPointer *FailureError
	if errors.As(err, &classifiedPointer) && classifiedPointer != nil {
		if classifiedPointer.RetryAfter > 0 {
			return classifiedPointer.RetryAfter
		}
	}
	var classifiedValue FailureError
	if errors.As(err, &classifiedValue) {
		if classifiedValue.RetryAfter > 0 {
			return classifiedValue.RetryAfter
		}
	}
	var rateLimitPointer *RateLimitError
	if errors.As(err, &rateLimitPointer) && rateLimitPointer != nil {
		if rateLimitPointer.RetryAfter > 0 {
			return rateLimitPointer.RetryAfter
		}
	}
	var rateLimitValue RateLimitError
	if errors.As(err, &rateLimitValue) {
		if rateLimitValue.RetryAfter > 0 {
			return rateLimitValue.RetryAfter
		}
	}
	var afterMarker interface{ RetryAfterDuration() time.Duration }
	if errors.As(err, &afterMarker) {
		after := afterMarker.RetryAfterDuration()
		if after > 0 {
			return after
		}
	}
	var methodMarker interface{ RetryAfter() time.Duration }
	if errors.As(err, &methodMarker) {
		after := methodMarker.RetryAfter()
		if after > 0 {
			return after
		}
	}
	if after := reflectedRetryAfter(err); after > 0 {
		return after
	}
	return 0
}

func reflectedRetryAfter(err error) time.Duration {
	durationType := reflect.TypeOf(time.Duration(0))
	for current := err; current != nil; current = errors.Unwrap(current) {
		value := reflect.ValueOf(current)
		for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
			if value.IsNil() {
				value = reflect.Value{}
				break
			}
			value = value.Elem()
		}
		if !value.IsValid() || value.Kind() != reflect.Struct {
			continue
		}
		retryAfter := value.FieldByName("RetryAfter")
		if !retryAfter.IsValid() || !retryAfter.CanInterface() || !retryAfter.Type().ConvertibleTo(durationType) {
			continue
		}
		delay := time.Duration(retryAfter.Convert(durationType).Int())
		if delay > 0 {
			return delay
		}
	}
	return 0
}

// Clock supplies time and a context-aware wait to workers. Keeping waiting
// behind the clock makes Run deterministic without sleeping in tests.
type Clock interface {
	Now() time.Time
	Wait(context.Context, time.Duration) error
}

// RealClock is the production Clock implementation.
type RealClock struct{}

func (RealClock) Now() time.Time {
	return time.Now().UTC()
}

func (RealClock) Wait(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// ClockFuncs adapts deterministic functions to Clock.
type ClockFuncs struct {
	NowFunc  func() time.Time
	WaitFunc func(context.Context, time.Duration) error
}

func (c ClockFuncs) Now() time.Time {
	if c.NowFunc == nil {
		return time.Now().UTC()
	}
	return c.NowFunc()
}

func (c ClockFuncs) Wait(ctx context.Context, delay time.Duration) error {
	if c.WaitFunc == nil {
		return RealClock{}.Wait(ctx, delay)
	}
	return c.WaitFunc(ctx, delay)
}

// Ensure an accidental formatting change does not turn a typed failure into a
// value that loses its underlying error.
var _ error = RetryableError{}
var _ error = TerminalError{}
var _ error = RateLimitError{}
var _ error = FailureError{}
