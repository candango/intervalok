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

func TestExponentialBackoffIntervalJitterBounds(t *testing.T) {
	t.Run("Should keep PercentJitter values within the configured band", func(t *testing.T) {
		bi := NewExponentialBackoffInterval(30*time.Second, "config", jitterTestConfig())
		jbi := NewExponentialBackoffInterval(30*time.Second, "config", jitterTestConfig())
		jbiConfig := jbi.Config.(*ExponentialConfig)
		jbiConfig.Jitter = true
		jbiConfig.JitterStrategy = PercentJitter

		cycles := 5
		for range jitterCyclePeriod * cycles {
			regularInterval := bi.Next()
			delta := DefaultRandomizer * float64(regularInterval)
			minInterval := time.Duration(float64(regularInterval) - delta)
			maxInterval := time.Duration(float64(regularInterval) + delta)
			jitterInterval := jbi.Next()
			assert.True(t, minInterval <= jitterInterval,
				"jitter %s below band min %s", jitterInterval, minInterval)
			assert.True(t, maxInterval >= jitterInterval,
				"jitter %s above band max %s", jitterInterval, maxInterval)
		}
		assert.Equal(t, cycles, jbi.State.(*ExponentialState).Cycles)
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
