package backoff

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBackoffIntervalWrapError(t *testing.T) {
	t.Run("Should wrap the error with the current interval", func(t *testing.T) {
		bi := NewBackoffInterval(time.Second * 2)
		original := errors.New("a message")
		wrapped := bi.WrapError(original)
		assert.EqualError(t, wrapped, "a message, backoff for 2s")
		assert.True(t, errors.Is(wrapped, original))
	})
}

func TestBackoffIntervalFixedDelay(t *testing.T) {
	t.Run("Should return the configured interval unchanged when no nextFunc is set", func(t *testing.T) {
		for i := range 50 {
			bi := NewBackoffInterval(time.Millisecond * time.Duration(i))
			assert.Equal(t, time.Millisecond*time.Duration(i), bi.Next())
		}
	})
}

func TestExponentialBackoffInterval(t *testing.T) {
	t.Run("Should ramp exponentially, cap at MaxInterval and cycle after MaxAttempts", func(t *testing.T) {
		bi := NewExponentialBackoffInterval(time.Second * 4)
		config := bi.Config.(*ExponentialConfig)
		state := bi.State.(*ExponentialState)
		cycles := 8
		for range 7 * 8 {
			fmt.Printf("Cycle %d: %s, %v\n", state.Cycles, bi.Current(), state.inProgress)
			expected := bi.InitialInterval
			if state.inProgress {
				expected = config.MaxInterval
				if float64(bi.Current()) < float64(config.MaxInterval)/config.Multiplier {
					expected = time.Duration(float64(bi.Current()) * config.Multiplier)
				}
			}
			next := bi.Next()
			fmt.Println("comparing: ", expected, next)
			assert.Equal(t, expected, next)
		}
		assert.Equal(t, cycles, state.Cycles)
	})
}

// jitterTestConfig is shared by the jitter tests below. With Multiplier=2.0,
// InitialInterval=30s and MaxInterval=3m, the interval ramps 30s, 60s, 120s,
// then stays capped at 3m for MaxAttempts calls before resetting -- an
// 11-call period per cycle (3 ramp-up calls + 8 capped calls).
func jitterTestConfig() *ExponentialConfig {
	return &ExponentialConfig{
		Multiplier:  DefaultMultiplier,
		Randomizer:  DefaultRandomizer,
		MaxInterval: 3 * time.Minute,
		MaxAttempts: 8,
	}
}

const jitterCyclePeriod = 11

// TestJitterFuncBounds validates each JitterFunc against its own strategy
// definition (not a re-derivation of the formula under test): FullJitter must
// stay in [0, interval], EqualJitter in [interval/2, interval], PercentJitter
// in [interval*(1-Randomizer), interval*(1+Randomizer)]. These are contract
// bounds independent of how each function draws its random value.
func TestJitterFuncBounds(t *testing.T) {
	interval := 30 * time.Second
	config := &ExponentialConfig{Randomizer: DefaultRandomizer}

	tests := []struct {
		name   string
		jitter JitterFunc
		lower  time.Duration
		upper  time.Duration
	}{
		{
			name:   "FullJitter",
			jitter: FullJitter,
			lower:  0,
			upper:  interval,
		},
		{
			name:   "EqualJitter",
			jitter: EqualJitter,
			lower:  interval / 2,
			upper:  interval,
		},
		{
			name:   "PercentJitter",
			jitter: PercentJitter,
			lower:  time.Duration(float64(interval) * (1 - DefaultRandomizer)),
			upper:  time.Duration(float64(interval) * (1 + DefaultRandomizer)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for range 1000 {
				got := tt.jitter(interval, config)
				assert.True(t, got >= tt.lower,
					"%s: %s below lower bound %s", tt.name, got, tt.lower)
				assert.True(t, got <= tt.upper,
					"%s: %s above upper bound %s", tt.name, got, tt.upper)
			}
		})
	}
}

// TestDecorrelatedNextFuncBounds validates DecorrelatedNextFunc's own
// recurrence contract: sleep = min(cap, random(base, prevSleep*3)), so every
// call must stay within [base, min(cap, prevSleep*3)] and never exceed cap.
func TestDecorrelatedNextFuncBounds(t *testing.T) {
	t.Run("Should keep each sleep within [base, min(cap, prevSleep*3)]", func(t *testing.T) {
		base := 1 * time.Second
		cap := 10 * time.Second
		bi := NewDecorrelatedBackoffInterval(base,
			"config", &DecorrelatedConfig{MaxInterval: cap})

		prevSleep := base
		for range 200 {
			upper := min(cap, prevSleep*3)
			got := bi.Next()
			assert.True(t, got >= base, "sleep %s below base %s", got, base)
			assert.True(t, got <= upper,
				"sleep %s above recurrence bound %s", got, upper)
			assert.True(t, got <= cap, "sleep %s above cap %s", got, cap)
			prevSleep = got
		}
	})
}

func TestExponentialBackoffIntervalJitterConcurrency(t *testing.T) {
	t.Run("Should stay consistent under concurrent access", func(t *testing.T) {
		jbi := NewExponentialBackoffInterval(30*time.Second, "config", jitterTestConfig())
		jbi.Config.(*ExponentialConfig).Jitter = true

		const goroutines = 8
		const perGoroutine = jitterCyclePeriod * 5
		maxPossibleCycles := (goroutines * perGoroutine) / jitterCyclePeriod

		var wg sync.WaitGroup
		for range goroutines {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range perGoroutine {
					jbi.Next()
				}
			}()
		}
		wg.Wait()

		state := jbi.State.(*ExponentialState)
		assert.True(t, state.Cycles > 0)
		assert.True(t, state.Cycles <= maxPossibleCycles)
	})
}
