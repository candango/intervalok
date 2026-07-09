package cron

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func mustParseTime(t *testing.T, layout, value string) time.Time {
	t.Helper()
	parsedTime, err := time.Parse(layout, value)
	if err != nil {
		t.Fatalf("Failed to parse time %s with layout %s: %v", value,
			layout, err)
	}
	return parsedTime
}

// TODO: We need to keep building the session engine tests
func TestCronSeries(t *testing.T) {
	layout := "2006-01-02 15:04:05"
	cases := []struct {
		name  string
		expr  string
		after string
		want  string
	}{
		{
			name:  "Every 5 minutes",
			expr:  "5 * * * *",
			after: "2025-08-15 12:01:00",
			want:  "2025-08-15 12:05:00",
		},
		{
			name:  "Fixed hour and minutes",
			expr:  "15 14 1 * *",
			after: "2025-08-15 12:01:00",
			want:  "2025-09-01 14:15:00",
		},
		{
			name:  "Step of 10 starting from 1",
			expr:  "1/10 * * * *",
			after: "2025-08-15 00:00:00",
			want:  "2025-08-15 00:01:00",
		},
		{
			name:  "Lists and ranges",
			expr:  "0,30 8-18 * * *",
			after: "2025-08-15 08:30:00",
			want:  "2025-08-15 09:00:00",
		},
		{
			name:  "New year",
			expr:  "0 0 1 1 *",
			after: "2025-12-31 23:59:00",
			want:  "2026-01-01 00:00:00",
		},
		{
			name:  "Next Sunday",
			expr:  "0 0 * * 0",
			after: "2025-08-13 00:00:00", // Wensday
			want:  "2025-08-17 00:00:00", // Next Sunday
		},
		{
			name:  "DOM and DOW restricted, DOM wins",
			expr:  "0 0 20 * 5",
			after: "2025-08-16 00:00:00", // Saturday
			want:  "2025-08-20 00:00:00", // Wednesday the 20th, before Friday the 22nd
		},
		{
			name:  "DOM and DOW restricted, DOW wins",
			expr:  "0 0 1 * 1",
			after: "2025-08-13 00:00:00", // Wednesday
			want:  "2025-08-18 00:00:00", // Monday the 18th, before September 1st
		},
		{
			name:  "DOM restricted only",
			expr:  "0 0 15 * *",
			after: "2025-08-13 00:00:00",
			want:  "2025-08-15 00:00:00",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			series, err := NewCronSeries(c.expr)
			if err != nil {
				t.Fatalf("failed to create cron series: %v", err)
			}
			after := mustParseTime(t, "2006-01-02 15:04:05", c.after)
			got := series.Next(after)
			want := mustParseTime(t, layout, c.want)
			assert.Equal(t, want, got)
		})
	}
}

func TestCronSeriesNoMatch(t *testing.T) {
	// February 31st never exists; Next must return the zero time instead
	// of searching forever.
	series, err := NewCronSeries("0 0 31 2 *")
	if err != nil {
		t.Fatalf("failed to create cron series: %v", err)
	}
	after := mustParseTime(t, "2006-01-02 15:04:05", "2025-08-13 00:00:00")
	assert.True(t, series.Next(after).IsZero())
}

func TestCronSeriesPrev(t *testing.T) {
	layout := "2006-01-02 15:04:05"
	cases := []struct {
		name   string
		expr   string
		before string
		want   string
	}{
		{
			name:   "Every 5 minutes",
			expr:   "5 * * * *",
			before: "2025-08-15 12:09:00",
			want:   "2025-08-15 12:05:00",
		},
		{
			name:   "On the exact match, excludes itself",
			expr:   "5 * * * *",
			before: "2025-08-15 12:05:00",
			want:   "2025-08-15 11:05:00",
		},
		{
			name:   "Fixed hour and minutes, previous month",
			expr:   "15 14 1 * *",
			before: "2025-09-15 12:01:00",
			want:   "2025-09-01 14:15:00",
		},
		{
			name:   "DOM and DOW restricted, most recent wins",
			expr:   "0 0 20 * 5",
			before: "2025-08-23 00:00:00",
			want:   "2025-08-22 00:00:00", // Friday the 22nd, after the 20th
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			series, err := NewCronSeries(c.expr)
			if err != nil {
				t.Fatalf("failed to create cron series: %v", err)
			}
			before := mustParseTime(t, layout, c.before)
			got := series.Prev(before)
			want := mustParseTime(t, layout, c.want)
			assert.Equal(t, want, got)
		})
	}
}

func TestCronSeriesPrevNoMatch(t *testing.T) {
	series, err := NewCronSeries("0 0 31 2 *")
	if err != nil {
		t.Fatalf("failed to create cron series: %v", err)
	}
	before := mustParseTime(t, "2006-01-02 15:04:05", "2025-08-13 00:00:00")
	assert.True(t, series.Prev(before).IsZero())
}

func TestCronSeriesUntilNext(t *testing.T) {
	series, err := NewCronSeries("5 * * * *")
	if err != nil {
		t.Fatalf("failed to create cron series: %v", err)
	}
	from := mustParseTime(t, "2006-01-02 15:04:05", "2025-08-15 12:01:00")
	got, err := series.UntilNext(from)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assert.Equal(t, 4*time.Minute, got)
}

func TestCronSeriesUntilNextError(t *testing.T) {
	series, err := NewCronSeries("0 0 31 2 *")
	if err != nil {
		t.Fatalf("failed to create cron series: %v", err)
	}
	from := mustParseTime(t, "2006-01-02 15:04:05", "2025-08-13 00:00:00")
	_, err = series.UntilNext(from)
	assert.Error(t, err)
}

func TestCronSeriesSincePrev(t *testing.T) {
	series, err := NewCronSeries("5 * * * *")
	if err != nil {
		t.Fatalf("failed to create cron series: %v", err)
	}
	from := mustParseTime(t, "2006-01-02 15:04:05", "2025-08-15 12:09:00")
	got, err := series.SincePrev(from)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assert.Equal(t, 4*time.Minute, got)
}

func TestCronSeriesSincePrevError(t *testing.T) {
	series, err := NewCronSeries("0 0 31 2 *")
	if err != nil {
		t.Fatalf("failed to create cron series: %v", err)
	}
	from := mustParseTime(t, "2006-01-02 15:04:05", "2025-08-13 00:00:00")
	_, err = series.SincePrev(from)
	assert.Error(t, err)
}
