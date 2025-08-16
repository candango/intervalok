package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CronSerie represents a parsed cron expression and stores allowed values for each field.
type CronSerie struct {
	minutes [60]bool // Allowed minutes (0-59)
	hours   [24]bool // Allowed hours (0-23)
	dom     [32]bool // Allowed days of month (1-31, 0 unused)
	months  [13]bool // Allowed months (1-12, 0 unused)
	dow     [7]bool  // Allowed days of week (0=Sunday)
	expr    string   // Original cron expression
}

// NewCronSerie parses a standard 5-field cron expression and returns a CronSerie.
// Returns an error if the expression is invalid.
func NewCronSerie(expr string) (*CronSerie, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("invalid cron expression: must have 5 fields")
	}
	c := &CronSerie{expr: expr}
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
func (c *CronSerie) Current(after time.Time) time.Time {
	return c.next(after)
}

// Next returns the next scheduled time after the provided time.
func (c *CronSerie) Next(after time.Time) time.Time {
	return c.next(after)
}

// next computes the next time that matches the cron schedule after the given
// time. It advances through each field in order: month, day, day-of-week,
// hour, minute. This version guarantees that the returned time is strictly
// after 'after'.
func (c *CronSerie) next(after time.Time) time.Time {
	t := after.Add(time.Minute).Truncate(time.Minute)
	for {
		// Advance month if not allowed or if t <= after
		if !c.months[int(t.Month())] || !t.After(after) {
			found := false
			for y := t.Year(); y <= t.Year()+5; y++ { // safety window
				startMonth := int(t.Month())
				if y > t.Year() {
					startMonth = 1
				}
				for m := startMonth; m <= 12; m++ {
					if c.months[m] {
						cand := time.Date(y, time.Month(m), 1, 0, 0, 0, 0, t.Location())
						if cand.After(after) {
							t = cand
							found = true
							break
						}
					}
				}
				if found {
					break
				}
			}
			continue
		}
		// Advance day of month if not allowed or if t <= after
		daysInCurrMonth := daysInMonth(t.Year(), t.Month())
		if !c.dom[t.Day()] || !t.After(after) {
			found := false
			for d := t.Day(); d <= daysInCurrMonth; d++ {
				if c.dom[d] {
					cand := time.Date(t.Year(), t.Month(), d, 0, 0, 0, 0, t.Location())
					if cand.After(after) {
						t = cand
						found = true
						break
					}
				}
			}
			if !found {
				// Go to the first day of next allowed month
				t = time.Date(t.Year(), t.Month(), daysInCurrMonth, 23, 59, 0, 0, t.Location()).Add(time.Minute)
			}
			continue
		}
		// Advance day of week if not allowed or if t <= after
		if !c.dow[int(t.Weekday())] || !t.After(after) {
			found := false
			for i := 1; i <= 7; i++ {
				nd := t.AddDate(0, 0, i)
				if c.dow[int(nd.Weekday())] && c.dom[nd.Day()] && c.months[int(nd.Month())] {
					if nd.After(after) {
						t = time.Date(nd.Year(), nd.Month(), nd.Day(), 0, 0, 0, 0, t.Location())
						found = true
						break
					}
				}
			}
			if !found {
				// Fallback: move to a future day.
				t = t.AddDate(0, 0, 7)
			}
			continue
		}
		// Advance hour if not allowed or if t <= after
		if !c.hours[t.Hour()] || !t.After(after) {
			found := false
			for h := t.Hour(); h < 24; h++ {
				if c.hours[h] {
					cand := time.Date(t.Year(), t.Month(), t.Day(), h, 0, 0, 0, t.Location())
					if cand.After(after) {
						t = cand
						found = true
						break
					}
				}
			}
			if !found {
				// Go to next day
				t = time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 0, 0, t.Location()).Add(time.Minute)
			}
			continue
		}
		// Advance minute if not allowed or if t <= after
		if !c.minutes[t.Minute()] || !t.After(after) {
			found := false
			for m := t.Minute(); m < 60; m++ {
				if c.minutes[m] {
					cand := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), m, 0, 0, t.Location())
					if cand.After(after) {
						t = cand
						found = true
						break
					}
				}
			}
			if !found {
				// Go to next hour
				t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 59, 0, 0, t.Location()).Add(time.Minute)
			}
			continue
		}
		// All fields match and t > after
		return t
	}
}

// daysInMonth returns the number of days in a given month of a specific year.
func daysInMonth(year int, month time.Month) int {
	switch month {
	case 4, 6, 9, 11:
		return 30
	case 2:
		if isLeap(year) {
			return 29
		}
		return 28
	default:
		return 31
	}
}

// isLeap returns true if the given year is a leap year.
func isLeap(year int) bool {
	return year%4 == 0 && (year%100 != 0 || year%400 == 0)
}
