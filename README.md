# intervalok

A Go library for series of time intervals: schedules that resolve in
`time.Duration`, ready for `time.Sleep`, retry loops, and schedulers.

Each series type shares the same duration-first shape: given a reference
`time.Time`, it tells you how long until the next occurrence (and, where it
makes sense, how long since the previous one). The calendar or backoff math
stays internal; the public contract is a duration.

## Packages

- [`cron`](./cron) — parses standard cron expressions and resolves the
  next/previous occurrence, in both absolute time and duration form.
- [`backoff`](./backoff) — pluggable exponential backoff strategies, with
  optional jitter, for retry policies.

<!-- TODO(cron): document usage, supported expression syntax (including
     aliases and named fields), and the duration-first API with examples.
     See cron/series.go for the current package doc. -->

### backoff

```go
bi := backoff.NewFullJitterBackoffInterval(500 * time.Millisecond)
for {
    if err := doSomething(); err != nil {
        time.Sleep(bi.Next())
        continue
    }
    break
}
```

`BackoffInterval` is a generic retry-delay engine: pluggable `nextFunc`/
`resetFunc` pairs drive the sequence, so new strategies plug in without new
concrete types. Jitter strategies follow the AWS Architecture Blog reference
["Exponential Backoff and Jitter"](https://aws.amazon.com/blogs/architecture/exponential-backoff-and-jitter/):

| Strategy | Formula | Constructor |
|---|---|---|
| `FullJitter` (default) | `random(0, interval)` | `NewFullJitterBackoffInterval` |
| `EqualJitter` | `interval/2 + random(0, interval/2)` | `NewEqualJitterBackoffInterval` |
| `DecorrelatedJitter` | `min(cap, random(base, prevSleep*3))` | `NewDecorrelatedBackoffInterval` |
| `PercentJitter` | `interval ± Randomizer%` (this package's own; not from the AWS reference) | `NewPercentJitterBackoffInterval` |

`DecorrelatedJitter` is structurally different from the other three: its
recurrence depends on the previously returned sleep instead of a
deterministic exponential ramp, so it runs as its own engine rather than a
jitter strategy plugged into the exponential one.

`WrapError` attaches the current backoff interval to an error, without
losing the original via `errors.Is`/`errors.As`:

```go
if err != nil {
    return bi.WrapError(err)
}
```

## Status

Early development. APIs may change without notice until a first tagged
release.

## License

MIT — see [LICENSE](./LICENSE).
