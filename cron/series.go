// Package cron parses standard cron expressions and resolves them against
// the calendar to answer "when is the next/previous occurrence" and "how
// long until/since it".
//
//	series, err := cron.NewCronSeries("*/15 * * * *") // every 15 minutes
//	if err != nil {
//	    return err
//	}
//	for {
//	    wait, err := series.UntilNext(time.Now())
//	    if err != nil {
//	        return err
//	    }
//	    time.Sleep(wait)
//	    doSomething()
//	}
//
// CronSeries is the entry point: parse an expression with NewCronSeries,
// then query it with Next/Prev (absolute time.Time) or UntilNext/SincePrev
// (time.Duration, as used above). CronSeries is stateless: every method is a
// pure function of its argument, so the same value can be shared across
// goroutines and queried repeatedly without synchronization.
//
// # Expression syntax
//
// A standard expression has 5 fields: minute, hour, day-of-month, month,
// day-of-week. Each field accepts a wildcard ("*"), a single value, a range
// ("1-5"), a step ("*/5", "1-30/5"), or a comma-separated list of any of
// those. Month and day-of-week fields also accept the standard three-letter
// names ("jan"-"dec", "sun"-"sat"), case-insensitively.
//
//   - A 6-field expression prepends seconds ("second minute hour
//     day-of-month month day-of-week"), raising the schedule's resolution
//     from minutes to seconds.
//   - A 7-field expression additionally appends a year field.
//   - This field order matches robfig/cron, the de facto standard Go cron
//     library; it differs from croniter, which appends seconds and year at
//     the end instead of prepending seconds.
//   - When both day-of-month and day-of-week are restricted (neither is
//     "*"), standard cron matches a day by their union: it fires when
//     either field matches, not only when both do. This is the same rule
//     vixie cron and its descendants use; see man 5 crontab.
//   - A leading "@" alias expands to a fixed expression before parsing:
//     @yearly/@annually ("0 0 1 1 *"), @monthly ("0 0 1 * *"), @weekly
//     ("0 0 * * 0"), @daily/@midnight ("0 0 * * *"), @hourly ("0 * * * *").
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
	seconds [60]bool    // Allowed seconds (0-59), only used when hasSeconds
	minutes [60]bool    // Allowed minutes (0-59)
	hours   [24]bool    // Allowed hours (0-23)
	dom     [32]bool    // Allowed days of month (1-31, 0 unused)
	months  [13]bool    // Allowed months (1-12, 0 unused)
	dow     [7]bool     // Allowed days of week (0=Sunday)
	years   [10000]bool // Allowed years (0-9999), only used when yearRestricted
	// domRestricted and dowRestricted record whether the day-of-month and
	// day-of-week fields were given as something other than '*'. Standard
	// cron matches a day by the union (OR) of both fields when both are
	// restricted, and by intersection otherwise.
	domRestricted bool
	// hasSeconds is true for 6- and 7-field expressions, raising the
	// schedule's resolution from minutes to seconds.
	hasSeconds    bool
	dowRestricted bool
	// yearRestricted is true for 7-field expressions with a year field
	// other than '*'.
	yearRestricted bool
	expr           string // Original cron expression
}

// NewCronSeries parses a cron expression and returns a CronSeries. The
// expression is one of the "@" aliases documented at the package level, a
// standard 5-field expression, a 6-field expression with a leading seconds
// field, or a 7-field expression additionally ending in a year field. It
// wraps ErrInvalidExpr if the expression is invalid.
func NewCronSeries(expr string) (*CronSeries, error) {
	resolved := expr
	if alias, ok := aliases[strings.ToLower(strings.TrimSpace(expr))]; ok {
		resolved = alias
	}
	fields := strings.Fields(resolved)

	var offset int
	hasSeconds := false
	switch len(fields) {
	case 5:
		offset = 0
	case 6, 7:
		offset = 1
		hasSeconds = true
	default:
		return nil, fmt.Errorf("%w: must have 5, 6 or 7 fields, got %d", ErrInvalidExpr, len(fields))
	}

	c := &CronSeries{expr: expr, hasSeconds: hasSeconds}
	if hasSeconds {
		if err := parseField(fields[0], 0, 59, c.seconds[:], nil); err != nil {
			return nil, fmt.Errorf("second: %w", err)
		}
	}
	if err := parseField(fields[offset], 0, 59, c.minutes[:], nil); err != nil {
		return nil, fmt.Errorf("minute: %w", err)
	}
	if err := parseField(fields[offset+1], 0, 23, c.hours[:], nil); err != nil {
		return nil, fmt.Errorf("hour: %w", err)
	}
	if err := parseField(fields[offset+2], 1, 31, c.dom[:], nil); err != nil {
		return nil, fmt.Errorf("day of month: %w", err)
	}
	if err := parseField(fields[offset+3], 1, 12, c.months[:], monthNames); err != nil {
		return nil, fmt.Errorf("month: %w", err)
	}
	if err := parseField(fields[offset+4], 0, 6, c.dow[:], dowNames); err != nil {
		return nil, fmt.Errorf("day of week: %w", err)
	}
	c.domRestricted = fields[offset+2] != "*"
	c.dowRestricted = fields[offset+4] != "*"

	if len(fields) == 7 {
		c.yearRestricted = fields[6] != "*"
		if c.yearRestricted {
			if err := parseField(fields[6], 0, len(c.years)-1, c.years[:], nil); err != nil {
				return nil, fmt.Errorf("year: %w", err)
			}
		}
	}
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

// Match reports whether t falls on a scheduled instant, at the schedule's
// own granularity: minute for a 5-field expression, second for a 6- or
// 7-field one (in which case seconds below that are ignored).
func (c *CronSeries) Match(t time.Time) bool {
	if !c.yearMatches(t.Year()) {
		return false
	}
	if !c.months[int(t.Month())] || !c.dayMatches(t) || !c.hours[t.Hour()] || !c.minutes[t.Minute()] {
		return false
	}
	return !c.hasSeconds || c.seconds[t.Second()]
}

// yearMatches reports whether year satisfies the year field, or is always
// true when the expression has no year field or the field is a wildcard.
func (c *CronSeries) yearMatches(year int) bool {
	if !c.yearRestricted {
		return true
	}
	if year < 0 || year >= len(c.years) {
		return false
	}
	return c.years[year]
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
// time. It advances through each field in order: year, month, day, hour,
// minute, second, jumping to the start of the next candidate period whenever
// a field does not match. The returned time is strictly after 'after'. If no
// match exists within a five year window (e.g. an impossible date), the zero
// time.Time is returned.
func (c *CronSeries) next(after time.Time) time.Time {
	var t time.Time
	if c.hasSeconds {
		t = after.Truncate(time.Second).Add(time.Second)
	} else {
		t = after.Truncate(time.Minute).Add(time.Minute)
	}
	limit := t.AddDate(5, 0, 0) // search window
	for t.Before(limit) {
		if !c.yearMatches(t.Year()) {
			// Jump to the first instant of the next year.
			t = time.Date(t.Year()+1, 1, 1, 0, 0, 0, 0, t.Location())
			continue
		}
		if !c.months[int(t.Month())] {
			// Jump to the first instant of the next month.
			t = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location()).AddDate(0, 1, 0)
			continue
		}
		if !c.dayMatches(t) {
			// Jump to the first instant of the next day.
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()).AddDate(0, 0, 1)
			continue
		}
		if !c.hours[t.Hour()] {
			// Jump to the first instant of the next hour.
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location()).Add(time.Hour)
			continue
		}
		if !c.minutes[t.Minute()] {
			// Jump to the first instant of the next minute.
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, t.Location()).Add(time.Minute)
			continue
		}
		if c.hasSeconds && !c.seconds[t.Second()] {
			t = t.Add(time.Second)
			continue
		}
		return t
	}
	return time.Time{}
}

// prev computes the last time that matches the cron schedule strictly before
// the given time. It mirrors next, walking backwards through year, month,
// day, hour, minute, and second. The returned time is strictly before
// 'before'. If no match exists within a five year window, the zero
// time.Time is returned.
func (c *CronSeries) prev(before time.Time) time.Time {
	// unit is the schedule's own granularity: every "jump to the edge of
	// the previous period" below lands exactly one unit before the start
	// of the current period, which is the true last instant of that
	// previous period regardless of how fine-grained unit is.
	unit := time.Minute
	if c.hasSeconds {
		unit = time.Second
	}

	// Truncate floors to the start of the unit. If 'before' sits exactly
	// on a unit boundary, that instant is not strictly before itself, so
	// step back one more unit; otherwise the floored instant already is.
	t := before.Truncate(unit)
	if t.Equal(before) {
		t = t.Add(-unit)
	}
	limit := t.AddDate(-5, 0, 0) // search window
	for t.After(limit) {
		if !c.yearMatches(t.Year()) {
			// Jump to the last instant of the previous year.
			t = time.Date(t.Year(), 1, 1, 0, 0, 0, 0, t.Location()).Add(-unit)
			continue
		}
		if !c.months[int(t.Month())] {
			// Jump to the last instant of the previous month.
			t = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location()).Add(-unit)
			continue
		}
		if !c.dayMatches(t) {
			// Jump to the last instant of the previous day.
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()).Add(-unit)
			continue
		}
		if !c.hours[t.Hour()] {
			// Jump to the last instant of the previous hour.
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location()).Add(-unit)
			continue
		}
		if !c.minutes[t.Minute()] {
			// Jump to the last instant of the previous minute.
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, t.Location()).Add(-unit)
			continue
		}
		if c.hasSeconds && !c.seconds[t.Second()] {
			t = t.Add(-unit)
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
