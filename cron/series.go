// Package cron parses standard cron expressions and resolves them against
// the calendar to answer "when is the next/previous occurrence" and "how
// long until/since it".
//
// CronSeries is the entry point: parse an expression with NewCronSeries,
// then query it with Next/Prev (absolute time.Time) or UntilNext/SincePrev
// (time.Duration, ready for time.Sleep or a retry loop). CronSeries is
// stateless: every method is a pure function of its argument, so the same
// value can be shared across goroutines and queried repeatedly without
// synchronization.
//
// # Expression syntax
//
// Expressions use the standard 5-field format: minute, hour, day-of-month,
// month, day-of-week. Each field accepts a wildcard ("*"), a single value, a
// range ("1-5"), a step ("*/5", "1-30/5"), or a comma-separated list of any
// of those. Month and day-of-week fields also accept the standard
// three-letter names ("jan"-"dec", "sun"-"sat"), case-insensitively.
//
// When both day-of-month and day-of-week are restricted (neither is "*"),
// standard cron matches a day by their union: it fires when either field
// matches, not only when both do. This is the same rule vixie cron and its
// descendants use; see man 5 crontab.
//
// A leading "@" alias expands to a fixed expression before parsing:
// @yearly/@annually ("0 0 1 1 *"), @monthly ("0 0 1 * *"), @weekly
// ("0 0 * * 0"), @daily/@midnight ("0 0 * * *"), @hourly ("0 * * * *").
//
// # Errors
//
// NewCronSeries wraps ErrInvalidExpr for any parse failure; check with
// errors.Is. UntilNext and SincePrev wrap ErrNoMatch when no occurrence
// exists within the search window (for example an impossible calendar date
// such as February 31st). Next and Prev have no error return: they signal
// the same condition with the zero time.Time, checkable with IsZero.
package cron

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ErrInvalidExpr is wrapped by errors returned when a cron expression fails
// to parse. Use errors.Is(err, ErrInvalidExpr) to detect parse failures.
var ErrInvalidExpr = errors.New("invalid cron expression")

// ErrNoMatch is wrapped by errors returned when no scheduled time exists
// within the search window, for example an impossible calendar date.
var ErrNoMatch = errors.New("no match found for expression")

// aliases maps predefined schedule shorthands to their 5-field equivalent.
var aliases = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
}

// monthNames and dowNames map the standard three-letter abbreviations to
// their numeric field value, allowing them in place of numbers.
var monthNames = map[string]int{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

var dowNames = map[string]int{
	"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
}

// CronSeries is a parsed cron expression. It holds the set of allowed values
// for each field and is safe for concurrent use: all of its methods are pure
// functions of their argument, with no shared mutable state.
type CronSeries struct {
	minutes [60]bool // Allowed minutes (0-59)
	hours   [24]bool // Allowed hours (0-23)
	dom     [32]bool // Allowed days of month (1-31, 0 unused)
	months  [13]bool // Allowed months (1-12, 0 unused)
	dow     [7]bool  // Allowed days of week (0=Sunday)
	// domRestricted and dowRestricted record whether the day-of-month and
	// day-of-week fields were given as something other than '*'. Standard
	// cron matches a day by the union (OR) of both fields when both are
	// restricted, and by intersection otherwise.
	domRestricted bool
	dowRestricted bool
	expr          string // Original cron expression
}

// NewCronSeries parses a standard 5-field cron expression, or one of the
// "@" aliases documented at the package level, and returns a CronSeries.
// It wraps ErrInvalidExpr if the expression is invalid.
func NewCronSeries(expr string) (*CronSeries, error) {
	resolved := expr
	if alias, ok := aliases[strings.ToLower(strings.TrimSpace(expr))]; ok {
		resolved = alias
	}
	fields := strings.Fields(resolved)
	if len(fields) != 5 {
		return nil, fmt.Errorf("%w: must have 5 fields, got %d", ErrInvalidExpr, len(fields))
	}
	c := &CronSeries{expr: expr}
	if err := parseField(fields[0], 0, 59, c.minutes[:], nil); err != nil {
		return nil, fmt.Errorf("minute: %w", err)
	}
	if err := parseField(fields[1], 0, 23, c.hours[:], nil); err != nil {
		return nil, fmt.Errorf("hour: %w", err)
	}
	if err := parseField(fields[2], 1, 31, c.dom[:], nil); err != nil {
		return nil, fmt.Errorf("day of month: %w", err)
	}
	if err := parseField(fields[3], 1, 12, c.months[:], monthNames); err != nil {
		return nil, fmt.Errorf("month: %w", err)
	}
	if err := parseField(fields[4], 0, 6, c.dow[:], dowNames); err != nil {
		return nil, fmt.Errorf("day of week: %w", err)
	}
	c.domRestricted = fields[2] != "*"
	c.dowRestricted = fields[4] != "*"
	return c, nil
}

// parseField populates the boolean array for a single cron field.
// Supports wildcards (*), ranges (x-y), steps (/), and comma-separated lists.
// When names is non-nil, tokens are also matched case-insensitively against
// it before being parsed as numbers (e.g. "jan" or "mon").
// Returns an error if the field is invalid.
func parseField(field string, min, max int, arr []bool, names map[string]int) error {
	parts := strings.Split(field, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		step := 1
		rangePart := part

		// Handle step values (e.g., */5 or 1-10/2)
		if strings.Contains(part, "/") {
			subs := strings.SplitN(part, "/", 2)
			rangePart = subs[0]
			var err error
			step, err = strconv.Atoi(subs[1])
			if err != nil || step <= 0 {
				return fmt.Errorf("%w: invalid step value: %s", ErrInvalidExpr, subs[1])
			}
		}

		var rmin, rmax int
		if rangePart == "*" || rangePart == "" {
			rmin = min
			rmax = max
		} else if strings.Contains(rangePart, "-") {
			bounds := strings.SplitN(rangePart, "-", 2)
			var ok1, ok2 bool
			rmin, ok1 = resolveToken(bounds[0], names)
			rmax, ok2 = resolveToken(bounds[1], names)
			if !ok1 || !ok2 || rmin > rmax || rmin < min || rmax > max {
				return fmt.Errorf("%w: invalid range: %s", ErrInvalidExpr, rangePart)
			}
		} else {
			val, ok := resolveToken(rangePart, names)
			if !ok || val < min || val > max {
				return fmt.Errorf("%w: invalid value: %s", ErrInvalidExpr, rangePart)
			}
			rmin, rmax = val, val
		}

		for i := rmin; i <= rmax; i += step {
			arr[i] = true
		}
	}
	return nil
}

// resolveToken resolves a single field token to its numeric value, checking
// names (case-insensitively) before falling back to plain integer parsing.
func resolveToken(tok string, names map[string]int) (int, bool) {
	if names != nil {
		if val, ok := names[strings.ToLower(tok)]; ok {
			return val, true
		}
	}
	val, err := strconv.Atoi(tok)
	if err != nil {
		return 0, false
	}
	return val, true
}

// Current is an alias for Next, kept for backwards compatibility. Prefer
// Next in new code.
func (c *CronSeries) Current(after time.Time) time.Time {
	return c.next(after)
}

// Next returns the next scheduled time strictly after the provided time. If
// no match exists within the search window, it returns the zero time.Time;
// check with IsZero.
func (c *CronSeries) Next(after time.Time) time.Time {
	return c.next(after)
}

// Prev returns the last scheduled time strictly before the provided time.
// If no match exists within the search window, it returns the zero
// time.Time; check with IsZero.
func (c *CronSeries) Prev(before time.Time) time.Time {
	return c.prev(before)
}

// Match reports whether t falls on a scheduled minute. Seconds and smaller
// are ignored, matching the schedule's minute granularity.
func (c *CronSeries) Match(t time.Time) bool {
	return c.months[int(t.Month())] && c.dayMatches(t) && c.hours[t.Hour()] && c.minutes[t.Minute()]
}

// MatchRange reports whether the schedule has an occurrence within [from,
// to], inclusive on both ends. It returns false if to is before from.
func (c *CronSeries) MatchRange(from, to time.Time) bool {
	if to.Before(from) {
		return false
	}
	if c.Match(from) {
		return true
	}
	occurrence := c.next(from)
	return !occurrence.IsZero() && !occurrence.After(to)
}

// UntilNext returns the duration from 'from' until the next scheduled time,
// equivalent to Next(from).Sub(from). It wraps ErrNoMatch if no match
// exists within the search window.
func (c *CronSeries) UntilNext(from time.Time) (time.Duration, error) {
	next := c.next(from)
	if next.IsZero() {
		return 0, fmt.Errorf("%w: %q", ErrNoMatch, c.expr)
	}
	return next.Sub(from), nil
}

// SincePrev returns the duration since the last scheduled time before
// 'from', equivalent to from.Sub(Prev(from)). It wraps ErrNoMatch if no
// match exists within the search window.
func (c *CronSeries) SincePrev(from time.Time) (time.Duration, error) {
	prev := c.prev(from)
	if prev.IsZero() {
		return 0, fmt.Errorf("%w: %q", ErrNoMatch, c.expr)
	}
	return from.Sub(prev), nil
}

// IsValid reports whether expr parses as a valid cron expression, without
// requiring a CronSeries to be kept around.
func IsValid(expr string) bool {
	_, err := NewCronSeries(expr)
	return err == nil
}

// next computes the next time that matches the cron schedule after the given
// time. It advances through each field in order: month, day, hour, minute,
// jumping to the start of the next candidate period whenever a field does not
// match. The returned time is strictly after 'after'. If no match exists
// within a five year window (e.g. an impossible date), the zero time.Time is
// returned.
func (c *CronSeries) next(after time.Time) time.Time {
	t := after.Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(5, 0, 0) // search window
	for t.Before(limit) {
		if !c.months[int(t.Month())] {
			// Jump to the first minute of the next month.
			t = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location()).AddDate(0, 1, 0)
			continue
		}
		if !c.dayMatches(t) {
			// Jump to the first minute of the next day.
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()).AddDate(0, 0, 1)
			continue
		}
		if !c.hours[t.Hour()] {
			// Jump to the first minute of the next hour.
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location()).Add(time.Hour)
			continue
		}
		if !c.minutes[t.Minute()] {
			t = t.Add(time.Minute)
			continue
		}
		return t
	}
	return time.Time{}
}

// prev computes the last time that matches the cron schedule strictly before
// the given time. It mirrors next, walking backwards through month, day,
// hour, and minute. The returned time is strictly before 'before'. If no
// match exists within a five year window, the zero time.Time is returned.
func (c *CronSeries) prev(before time.Time) time.Time {
	// Truncate floors to the start of the minute. If 'before' sits exactly
	// on a minute boundary, that minute is not strictly before itself, so
	// step back one more minute; otherwise the floored minute already is.
	t := before.Truncate(time.Minute)
	if t.Equal(before) {
		t = t.Add(-time.Minute)
	}
	limit := t.AddDate(-5, 0, 0) // search window
	for t.After(limit) {
		if !c.months[int(t.Month())] {
			// Jump to the last minute of the previous month.
			t = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location()).Add(-time.Minute)
			continue
		}
		if !c.dayMatches(t) {
			// Jump to the last minute of the previous day.
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()).Add(-time.Minute)
			continue
		}
		if !c.hours[t.Hour()] {
			// Jump to the last minute of the previous hour.
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location()).Add(-time.Minute)
			continue
		}
		if !c.minutes[t.Minute()] {
			t = t.Add(-time.Minute)
			continue
		}
		return t
	}
	return time.Time{}
}

// dayMatches reports whether t's day satisfies the day-of-month and
// day-of-week fields. Standard cron semantics: when both fields are
// restricted (neither is '*'), the day matches if either field matches;
// otherwise both must match (a wildcard field matches every day anyway).
func (c *CronSeries) dayMatches(t time.Time) bool {
	dom := c.dom[t.Day()]
	dow := c.dow[int(t.Weekday())]
	if c.domRestricted && c.dowRestricted {
		return dom || dow
	}
	return dom && dow
}
