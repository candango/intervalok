package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CronSeries represents a parsed cron expression and stores allowed values for each field.
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

// NewCronSeries parses a standard 5-field cron expression and returns a CronSeries.
// Returns an error if the expression is invalid.
func NewCronSeries(expr string) (*CronSeries, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("invalid cron expression: must have 5 fields")
	}
	c := &CronSeries{expr: expr}
	if err := parseField(fields[0], 0, 59, c.minutes[:]); err != nil {
		return nil, fmt.Errorf("minute: %w", err)
	}
	if err := parseField(fields[1], 0, 23, c.hours[:]); err != nil {
		return nil, fmt.Errorf("hour: %w", err)
	}
	if err := parseField(fields[2], 1, 31, c.dom[:]); err != nil {
		return nil, fmt.Errorf("day of month: %w", err)
	}
	if err := parseField(fields[3], 1, 12, c.months[:]); err != nil {
		return nil, fmt.Errorf("month: %w", err)
	}
	if err := parseField(fields[4], 0, 6, c.dow[:]); err != nil {
		return nil, fmt.Errorf("day of week: %w", err)
	}
	c.domRestricted = fields[2] != "*"
	c.dowRestricted = fields[4] != "*"
	return c, nil
}

// parseField populates the boolean array for a single cron field.
// Supports wildcards (*), ranges (x-y), steps (/), and comma-separated lists.
// Returns an error if the field is invalid.
func parseField(field string, min, max int, arr []bool) error {
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
				return fmt.Errorf("invalid step value: %s", subs[1])
			}
		}

		var rmin, rmax int
		if rangePart == "*" || rangePart == "" {
			rmin = min
			rmax = max
		} else if strings.Contains(rangePart, "-") {
			bounds := strings.SplitN(rangePart, "-", 2)
			var err1, err2 error
			rmin, err1 = strconv.Atoi(bounds[0])
			rmax, err2 = strconv.Atoi(bounds[1])
			if err1 != nil || err2 != nil || rmin > rmax || rmin < min || rmax > max {
				return fmt.Errorf("invalid range: %s", rangePart)
			}
		} else {
			val, err := strconv.Atoi(rangePart)
			if err != nil || val < min || val > max {
				return fmt.Errorf("invalid value: %s", rangePart)
			}
			rmin, rmax = val, val
		}

		for i := rmin; i <= rmax; i += step {
			arr[i] = true
		}
	}
	return nil
}

// Current returns the next scheduled time after the provided time.
func (c *CronSeries) Current(after time.Time) time.Time {
	return c.next(after)
}

// Next returns the next scheduled time after the provided time.
func (c *CronSeries) Next(after time.Time) time.Time {
	return c.next(after)
}

// next computes the next time that matches the cron schedule after the given
// time. It advances through each field in order: month, day, hour, minute,
// jumping to the start of the next candidate period whenever a field does not
// match. The returned time is strictly after 'after'. If no match exists
// within a five year window (e.g. an impossible date), the zero time.Time is
// returned.
func (c *CronSeries) next(after time.Time) time.Time {
	t := after.Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(5, 0, 0) // safety window
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
