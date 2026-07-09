// Package backoff provides pluggable exponential backoff strategies, with
// optional jitter, for retry policies.
//
// Jitter strategies are modeled after the AWS Architecture Blog reference
// "Exponential Backoff and Jitter":
// https://aws.amazon.com/blogs/architecture/exponential-backoff-and-jitter/
// FullJitter implements that article's recommended (and, per its own
// simulation, most effective) strategy and is this package's default.
// PercentJitter is this package's own narrower-band alternative and is not
// one of the strategies the article names.
package backoff

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"time"
)

// DefaultRandomizer is the default jitter magnitude for ExponentialBackoff
// specifying the relative deviation (e.g., 0.25 means ±25% of the interval).
// It controls the randomness added to desynchronize retries.
const DefaultRandomizer = 0.25

// DefaultMultiplier is the default factor by which the interval grows in
// ExponentialBackoff (e.g., 2.0 means each attempt’s delay is 2.0 times the
// previous one). It determines the exponential growth rate. While 2.0 is a
// classic default in many sources, modern AWS SDKs and some HTTP clients often
// use 1.5 as a less aggressive default (to avoid excessive wait times early).
// See ModerateBackoffMultiplier for a gentler alternative.
const DefaultMultiplier = 2.0

// ModerateMultiplier is an alternative multiplier for
// ExponentialBackoff, increasing the interval by 1.5x each attempt for a
// gentler escalation compared to BackoffMultiplier. Use this for scenarios
// where slower retry growth is preferred, such as less contended systems.
const ModerateMultiplier = 1.5

// DefaultStartInterval is the default starting delay for ExponentialBackoff
// (e.g., 500ms for the first retry). It sets the baseline for the backoff
// sequence.
const DefaultStartInterval = 250 * time.Millisecond

// DefaultMaxInterval is the default maximum delay for ExponentialBackoff
// (e.g., 1 minute). It caps the interval to prevent excessively long retries.
const DefaultMaxInterval = 1 * time.Minute

// DefaultMaxAttempts is the default number of attempts at DefaultMaxInterval
// before resetting ExponentialBackoff to its initial interval (e.g., 3
// attempts). It controls how long the backoff persists at the maximum delay.
const DefaultMaxAttempts = 3

// Backoff defines an interface for backoff strategies that generate retry
// delays.
// Implementations provide the next delay duration and a method to reset the
// backoff state.
type Backoff interface {
	Current() time.Duration

	// Next returns the duration to wait before the next retry attempt.
	// The duration may vary depending on the backoff strategy (e.g., fixed,
	// exponential).
	Next() time.Duration

	// Reset resets the backoff state to its initial configuration, allowing
	// the backoff sequence to start anew.
	Reset()

	// WrapError wraps err with the current backoff interval, so callers can
	// surface how long the next retry will wait without losing the original
	// error via errors.Is/errors.As.
	WrapError(err error) error
}

// BackoffInterval implements a flexible backoff mechanism with pluggable logic
// for generating the next interval and resetting state.
type BackoffInterval struct {
	// Interval is the current Interval duration, used as the default if no
	// nextFunc is set.
	Interval time.Duration

	// InitialInterval is the initial interval, used as the default if no
	// nextFunc is set.
	InitialInterval time.Duration

	// resetFunc resets the backoff state to its initial configuration.
	resetFunc func(*BackoffInterval)

	// nextFunc computes the next backoff interval; can implement custom or
	// exponential logic.
	nextFunc func(*BackoffInterval) time.Duration

	// mutex ensures thread-safe updates to the backoff state.
	mutex sync.Mutex

	// Config is an optional configuration object (e.g., *ExponentialConfig)
	// associated with the backoff logic.
	Config any

	// State is an optional state object (e.g., *ExponentialState) for
	// maintaining backoff progress.
	State any
}

// NextFunc defines a function that, given a BackoffInterval, computes and
// returns the next interval duration.
type NextFunc func(*BackoffInterval) time.Duration

// ResetFunc defines a function that, given a BackoffInterval, resets its
// internal state to the initial configuration.
type ResetFunc func(*BackoffInterval)

// NewBackoffInterval creates a new BackoffInterval with the specified initial
// interval. By default, the returned BackoffInterval has no nextFunc or
// resetFunc set, so it will always return the provided interval unless those
// are configured.
func NewBackoffInterval(interval time.Duration) *BackoffInterval {
	return &BackoffInterval{
		Interval:        interval,
		InitialInterval: interval,
		mutex:           sync.Mutex{},
	}
}

// WithConfig sets the configuration object for the BackoffInterval and returns
// the instance for chaining.
//
// The config can be any type (e.g., *ExponentialConfig).
func (b *BackoffInterval) WithConfig(config any) *BackoffInterval {
	b.Config = config
	return b
}

// WithState sets the state object for the BackoffInterval and returns the
// instance for chaining.
//
// The state can be any type (e.g., *ExponentialState).
func (b *BackoffInterval) WithState(state any) *BackoffInterval {
	b.State = state
	return b
}

// WithNextFunc sets the function that computes the next interval for the
// BackoffInterval and returns the instance for chaining.
func (b *BackoffInterval) WithNextFunc(nextFunc NextFunc) *BackoffInterval {
	b.nextFunc = nextFunc
	return b
}

// WithResetFunc sets the function that resets the BackoffInterval's state to
// its initial configuration and returns the instance for chaining.
func (b *BackoffInterval) WithResetFunc(resetFunc ResetFunc) *BackoffInterval {
	b.resetFunc = resetFunc
	return b
}

// Current returns the current interval duration for this backoff instance.
// This method is thread-safe.
func (b *BackoffInterval) Current() time.Duration {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.Interval
}

// Reset resets the backoff state to its initial configuration, allowing the
// backoff sequence to start anew. If no resetFunc is configured, this is a
// no-op.
func (b *BackoffInterval) Reset() {
	if b.resetFunc != nil {
		b.resetFunc(b)
	}
}

// Next returns the duration to wait before the next retry attempt by invoking
// the configured nextFunc. If no nextFunc is set, it returns the current
// interval as a fixed delay.
func (b *BackoffInterval) Next() time.Duration {
	if b.nextFunc != nil {
		return b.nextFunc(b)
	}
	return b.Interval
}

// WrapError wraps err with the current backoff interval using %w, so the
// original error remains inspectable via errors.Is/errors.As while the
// message carries how long the next retry will wait.
func (b *BackoffInterval) WrapError(err error) error {
	return fmt.Errorf("%w, backoff for %s", err, b.Current())
}

// JitterFunc computes a jittered duration from the exponentially-ramped,
// already-capped interval. Implementations may consult config (e.g.
// Randomizer for PercentJitter) but must not mutate it.
type JitterFunc func(interval time.Duration, config *ExponentialConfig) time.Duration

// PercentJitter randomizes interval within a symmetric band of
// ±config.Randomizer (e.g. 0.25 means ±25%). If Randomizer <= 0, interval is
// returned unchanged. This is the package's original jitter formula; it does
// not match any of the strategies named in the AWS "Exponential Backoff and
// Jitter" reference (see FullJitter), but is kept as an explicit opt-in.
func PercentJitter(interval time.Duration, config *ExponentialConfig) time.Duration {
	if config.Randomizer <= 0 {
		return interval
	}
	magnitude := config.Randomizer * float64(interval)
	minVal := float64(interval) - magnitude
	maxVal := float64(interval) + magnitude
	return time.Duration(minVal + (rand.Float64() * (maxVal - minVal)))
}

// FullJitter implements the "Full Jitter" strategy from the AWS
// "Exponential Backoff and Jitter" reference
// (https://aws.amazon.com/blogs/architecture/exponential-backoff-and-jitter/):
// sleep = random(0, interval). It desynchronizes retrying clients more
// effectively than PercentJitter, including on the very first (cold-start)
// attempt, and is the package's default jitter strategy.
func FullJitter(interval time.Duration, _ *ExponentialConfig) time.Duration {
	return time.Duration(rand.Float64() * float64(interval))
}

// EqualJitter implements the "Equal Jitter" strategy from the AWS
// "Exponential Backoff and Jitter" reference
// (https://aws.amazon.com/blogs/architecture/exponential-backoff-and-jitter/):
// sleep = half + random(0, half), where half = interval/2. It always keeps
// at least half of the computed interval, trading some of FullJitter's
// desynchronization power for a wait that never collapses close to zero --
// useful when a very short retry would still likely fail (e.g. the remote
// side needs a minimum recovery time).
func EqualJitter(interval time.Duration, _ *ExponentialConfig) time.Duration {
	half := interval / 2
	return half + time.Duration(rand.Float64()*float64(half))
}

// ExponentialConfig holds configuration parameters for an exponential backoff
// strategy.
type ExponentialConfig struct {
	// Jitter determines whether to apply randomization to each interval.
	Jitter bool

	// JitterStrategy selects the randomization formula applied when Jitter is
	// true. If nil, FullJitter is used.
	JitterStrategy JitterFunc

	// MaxAttempts is the number of attempts to use the maximum interval before
	// resetting.
	MaxAttempts int

	// MaxInterval caps the maximum delay between retries.
	MaxInterval time.Duration

	// Multiplier is the factor by which the interval increases after each
	// retry.
	Multiplier float64

	// Randomizer specifies the magnitude of jitter as a fraction of the
	// interval (e.g., 0.25 means ±25%). Only consulted by PercentJitter.
	Randomizer float64
}

// ExponentialState tracks the runtime state of an exponential backoff
// sequence.
type ExponentialState struct {
	// Cycles counts the number of times the backoff sequence has reached maxed
	// out attempts and reset.
	Cycles int

	// inProgress indicates if the backoff sequence is currently active.
	inProgress bool

	// statedAt records the time the current backoff cycle started.
	statedAt time.Time

	// maxedOutCycles counts consecutive uses of MaxInterval.
	maxedOutCycles int
}

// ensureExponentialDefaults initializes the Config and State fields of the
// given BackoffInterval with default exponential backoff values if they are
// nil. This function is useful to guarantee that BackoffInterval is always in
// a valid state before use. Not thread-safe! Caller must lock if concurrent
// access is possible.
//
// Example usage:
//
//	ensureExponentialDefaults(b)
func ensureExponentialDefaults(b *BackoffInterval) {
	if b.Config == nil {
		b.Config = &ExponentialConfig{
			MaxInterval: DefaultMaxInterval,
			Multiplier:  DefaultMultiplier,
			Randomizer:  DefaultRandomizer,
			MaxAttempts: DefaultMaxAttempts,
		}
	}
	if b.State == nil {
		b.State = &ExponentialState{
			maxedOutCycles: 0,
			statedAt:       time.Now(),
		}
	}
}

// mustExponentialConfigAndState returns the *ExponentialConfig and
// *ExponentialState of b, locking b.mutex during access. Panics if the types
// are not correct. Not thread-safe!
func mustExponentialConfigAndState(b *BackoffInterval) (*ExponentialConfig,
	*ExponentialState) {
	ensureExponentialDefaults(b)
	config, ok := b.Config.(*ExponentialConfig)
	if !ok {
		panic("config must be *ExponentialConfig")
	}
	state, ok := b.State.(*ExponentialState)
	if !ok {
		panic("state must be *ExponentialState")
	}
	return config, state
}

// ExponentialNextFunc implements the exponential backoff logic for
// BackoffInterval. It returns the next interval duration for a retry attempt,
// updating the internal state accordingly. The interval increases
// exponentially with each call, up to the configured maximum. If the maximum
// interval is reached for a number of attempts specified by MaxAttempts, the
// cycle is reset and begins again from the initial interval. If jitter is
// enabled (Jittered == true), the interval is randomized within a range
// defined by the Randomizer setting to help prevent synchronized retries.
//
// Thread-safe: locks the BackoffInterval's mutex to ensure consistent state
// updates.
func ExponentialNextFunc(b *BackoffInterval) time.Duration {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	config, state := mustExponentialConfigAndState(b)

	var ret time.Duration

	if !state.inProgress {
		state.inProgress = true
		b.Interval = b.InitialInterval
		ret = b.Interval
	} else {
		// Advance before returning
		nextInterval :=
			min(time.Duration(float64(b.Interval)*config.Multiplier),
				config.MaxInterval)
		b.Interval = nextInterval
		ret = b.Interval
	}

	if b.Interval == config.MaxInterval {
		state.maxedOutCycles++
	} else {
		state.maxedOutCycles = 0
	}

	if state.maxedOutCycles >= config.MaxAttempts {
		state.Cycles++
		b.Interval = b.InitialInterval
		state.inProgress = false
		state.maxedOutCycles = 0
	}

	// Apply jitter if configured
	if config.Jitter {
		strategy := config.JitterStrategy
		if strategy == nil {
			strategy = FullJitter
		}
		ret = strategy(ret, config)
	}
	return ret
}

// ExponentialResetFunc resets the state of an Exponential backoff sequence to
// the initial configuration. It sets the interval and counters back to their
// starting values, so the next call will begin anew. Thread-safe: locks the
// BackoffInterval's mutex to ensure consistent state updates.
func ExponentialResetFunc(b *BackoffInterval) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	_, state := mustExponentialConfigAndState(b)
	state.inProgress = false
	b.Interval = b.InitialInterval
	state.maxedOutCycles = 0
}

// NewExponentialBackoffInterval constructs a new BackoffInterval using
// exponential settings. The first argument is the initial interval (required).
// You may override config/state by passing string-keyed options, e.g.:
//
//	NewExponentialBackoffInterval(
//	    10*time.Second,
//	    "config", &ExponentialConfig{...},
//	    "state", &ExponentialState{...},
//	)
func NewExponentialBackoffInterval(interval time.Duration,
	options ...any) *BackoffInterval {
	bi := NewBackoffInterval(interval).
		WithNextFunc(ExponentialNextFunc).
		WithResetFunc(ExponentialResetFunc)
	ensureExponentialDefaults(bi)
	if len(options)%2 != 0 {
		panic("options must be even: key-value pairs")
	}
	for i := 0; i < len(options); i += 2 {
		key, ok := options[i].(string)
		if !ok {
			panic("options must be strings")
		}
		value := options[i+1]
		switch strings.Trim(strings.ToLower(key), "") {
		case "config":
			cfg, ok := value.(*ExponentialConfig)
			if !ok {
				panic("value for 'config' must be *ExponentialConfig")
			}
			bi = bi.WithConfig(cfg)
		case "state":
			st, ok := value.(*ExponentialState)
			if !ok {
				panic("value for 'state' must be *ExponentialState")
			}
			bi = bi.WithState(st)
		}
	}
	return bi
}

// NewJitterBackoffInterval constructs an exponential backoff with jitter
// enabled using whichever JitterStrategy the caller configures (via the
// "config" option); if none is set, it defaults to FullJitter. Prefer the
// explicit NewFullJitterBackoffInterval / NewPercentJitterBackoffInterval
// constructors when you want a specific strategy without reaching into
// Config yourself.
func NewJitterBackoffInterval(interval time.Duration,
	options ...any) *BackoffInterval {
	bi := NewExponentialBackoffInterval(interval, options...)
	if cfg, ok := bi.Config.(*ExponentialConfig); ok {
		cfg.Jitter = true
	}
	return bi
}

// NewFullJitterBackoffInterval constructs an exponential backoff using the
// "Full Jitter" strategy (see FullJitter): each wait is randomized across the
// entire range [0, interval]. This spreads out retrying clients the most and
// is the strategy AWS's "Exponential Backoff and Jitter" reference found most
// effective at reducing total client/server work -- use it as the default
// choice for retrying against a shared/remote resource unless you have a
// specific reason to prefer a narrower spread.
func NewFullJitterBackoffInterval(interval time.Duration,
	options ...any) *BackoffInterval {
	bi := NewExponentialBackoffInterval(interval, options...)
	if cfg, ok := bi.Config.(*ExponentialConfig); ok {
		cfg.Jitter = true
		cfg.JitterStrategy = FullJitter
	}
	return bi
}

// NewPercentJitterBackoffInterval constructs an exponential backoff using the
// "Percent Jitter" strategy (see PercentJitter): each wait stays within
// ±Randomizer of the computed interval instead of ranging down to zero. It
// desynchronizes retries less aggressively than FullJitter, but keeps waits
// close to the "expected" backoff curve, which is useful when a caller (or a
// human reading logs) needs the wait time to stay roughly predictable.
func NewPercentJitterBackoffInterval(interval time.Duration,
	options ...any) *BackoffInterval {
	bi := NewExponentialBackoffInterval(interval, options...)
	if cfg, ok := bi.Config.(*ExponentialConfig); ok {
		cfg.Jitter = true
		cfg.JitterStrategy = PercentJitter
	}
	return bi
}

// NewEqualJitterBackoffInterval constructs an exponential backoff using the
// "Equal Jitter" strategy (see EqualJitter): each wait stays within
// [interval/2, interval] instead of ranging down to zero. Prefer this over
// FullJitter when a very short wait would still likely fail against the
// resource being retried.
func NewEqualJitterBackoffInterval(interval time.Duration,
	options ...any) *BackoffInterval {
	bi := NewExponentialBackoffInterval(interval, options...)
	if cfg, ok := bi.Config.(*ExponentialConfig); ok {
		cfg.Jitter = true
		cfg.JitterStrategy = EqualJitter
	}
	return bi
}

// DecorrelatedConfig holds configuration parameters for
// DecorrelatedNextFunc.
type DecorrelatedConfig struct {
	// MaxInterval caps the maximum delay between retries.
	MaxInterval time.Duration
}

// DecorrelatedState tracks the runtime state of a Decorrelated Jitter
// sequence: the previous sleep value the recurrence is based on.
type DecorrelatedState struct {
	sleep time.Duration
}

// ensureDecorrelatedDefaults initializes the Config and State fields of the
// given BackoffInterval with default Decorrelated Jitter values if they are
// nil. Not thread-safe! Caller must lock if concurrent access is possible.
func ensureDecorrelatedDefaults(b *BackoffInterval) {
	if b.Config == nil {
		b.Config = &DecorrelatedConfig{MaxInterval: DefaultMaxInterval}
	}
	if b.State == nil {
		b.State = &DecorrelatedState{sleep: b.InitialInterval}
	}
}

// mustDecorrelatedConfigAndState returns the *DecorrelatedConfig and
// *DecorrelatedState of b. Panics if the types are not correct. Not
// thread-safe!
func mustDecorrelatedConfigAndState(b *BackoffInterval) (*DecorrelatedConfig,
	*DecorrelatedState) {
	ensureDecorrelatedDefaults(b)
	config, ok := b.Config.(*DecorrelatedConfig)
	if !ok {
		panic("config must be *DecorrelatedConfig")
	}
	state, ok := b.State.(*DecorrelatedState)
	if !ok {
		panic("state must be *DecorrelatedState")
	}
	return config, state
}

// DecorrelatedNextFunc implements the "Decorrelated Jitter" strategy from the
// AWS "Exponential Backoff and Jitter" reference
// (https://aws.amazon.com/blogs/architecture/exponential-backoff-and-jitter/):
//
//	sleep = min(cap, random(base, sleep_prev * 3))
//
// Unlike ExponentialNextFunc, there is no deterministic ramp separate from
// the jitter: each call's randomization is derived directly from the
// previously returned sleep, not from a Multiplier-based doubling. The AWS
// reference defines no MaxAttempts/Cycles reset for this strategy, so none is
// applied here -- the sequence runs indefinitely.
//
// Thread-safe: locks the BackoffInterval's mutex to ensure consistent state
// updates.
func DecorrelatedNextFunc(b *BackoffInterval) time.Duration {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	config, state := mustDecorrelatedConfigAndState(b)

	base := float64(b.InitialInterval)
	upper := float64(state.sleep) * 3
	next := time.Duration(base + rand.Float64()*(upper-base))
	if next > config.MaxInterval {
		next = config.MaxInterval
	}

	state.sleep = next
	b.Interval = next
	return next
}

// DecorrelatedResetFunc resets a Decorrelated Jitter sequence's sleep back to
// the initial interval. Thread-safe: locks the BackoffInterval's mutex to
// ensure consistent state updates.
func DecorrelatedResetFunc(b *BackoffInterval) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	_, state := mustDecorrelatedConfigAndState(b)
	state.sleep = b.InitialInterval
	b.Interval = b.InitialInterval
}

// NewDecorrelatedBackoffInterval constructs a new BackoffInterval using the
// Decorrelated Jitter strategy. The first argument is the initial interval,
// used as both the recurrence's base and the starting sleep (required). You
// may override config/state by passing string-keyed options, e.g.:
//
//	NewDecorrelatedBackoffInterval(
//	    10*time.Second,
//	    "config", &DecorrelatedConfig{...},
//	)
func NewDecorrelatedBackoffInterval(interval time.Duration,
	options ...any) *BackoffInterval {
	bi := NewBackoffInterval(interval).
		WithNextFunc(DecorrelatedNextFunc).
		WithResetFunc(DecorrelatedResetFunc)
	ensureDecorrelatedDefaults(bi)
	if len(options)%2 != 0 {
		panic("options must be even: key-value pairs")
	}
	for i := 0; i < len(options); i += 2 {
		key, ok := options[i].(string)
		if !ok {
			panic("options must be strings")
		}
		value := options[i+1]
		switch strings.Trim(strings.ToLower(key), "") {
		case "config":
			cfg, ok := value.(*DecorrelatedConfig)
			if !ok {
				panic("value for 'config' must be *DecorrelatedConfig")
			}
			bi = bi.WithConfig(cfg)
		case "state":
			st, ok := value.(*DecorrelatedState)
			if !ok {
				panic("value for 'state' must be *DecorrelatedState")
			}
			bi = bi.WithState(st)
		}
	}
	return bi
}
