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

func TestCronSeriesNext(t *testing.T) {
	t.Run("Should advance to the next matching time for a range of expressions", func(t *testing.T) {
		layout := "2006-01-02 15:04:05"
		cases := []struct {
			name  string
			expr  string
			after string
			want  string
		}{
			{
				name:  "every 5 minutes",
				expr:  "5 * * * *",
				after: "2025-08-15 12:01:00",
				want:  "2025-08-15 12:05:00",
			},
			{
				name:  "fixed hour and minute",
				expr:  "15 14 1 * *",
				after: "2025-08-15 12:01:00",
				want:  "2025-09-01 14:15:00",
			},
			{
				name:  "step starting from an offset",
				expr:  "1/10 * * * *",
				after: "2025-08-15 00:00:00",
				want:  "2025-08-15 00:01:00",
			},
			{
				name:  "minute lists and hour ranges",
				expr:  "0,30 8-18 * * *",
				after: "2025-08-15 08:30:00",
				want:  "2025-08-15 09:00:00",
			},
			{
				name:  "rolls over into the next year",
				expr:  "0 0 1 1 *",
				after: "2025-12-31 23:59:00",
				want:  "2026-01-01 00:00:00",
			},
			{
				name:  "next Sunday",
				expr:  "0 0 * * 0",
				after: "2025-08-13 00:00:00", // Wednesday
				want:  "2025-08-17 00:00:00", // Next Sunday
			},
			{
				// Saturday the 16th; the 20th (Wednesday) comes before
				// Friday the 22nd, so day-of-month wins the union.
				name:  "dom and dow restricted, dom wins",
				expr:  "0 0 20 * 5",
				after: "2025-08-16 00:00:00",
				want:  "2025-08-20 00:00:00",
			},
			{
				// Wednesday the 13th; Monday the 18th comes before
				// September 1st, so day-of-week wins the union.
				name:  "dom and dow restricted, dow wins",
				expr:  "0 0 1 * 1",
				after: "2025-08-13 00:00:00",
				want:  "2025-08-18 00:00:00",
			},
			{
				name:  "dom restricted only",
				expr:  "0 0 15 * *",
				after: "2025-08-13 00:00:00",
				want:  "2025-08-15 00:00:00",
			},
		}

		for _, c := range cases {
			series, err := NewCronSeries(c.expr)
			if err != nil {
				t.Fatalf("%s: failed to create cron series: %v", c.name, err)
			}
			after := mustParseTime(t, layout, c.after)
			got := series.Next(after)
			want := mustParseTime(t, layout, c.want)
			assert.Equal(t, want, got, c.name)
		}
	})
}

func TestCronSeriesNextNoMatch(t *testing.T) {
	t.Run("Should return the zero time when no match exists in the search window", func(t *testing.T) {
		// February 31st never exists.
		series, err := NewCronSeries("0 0 31 2 *")
		if err != nil {
			t.Fatalf("failed to create cron series: %v", err)
		}
		after := mustParseTime(t, "2006-01-02 15:04:05", "2025-08-13 00:00:00")
		assert.True(t, series.Next(after).IsZero())
	})
}

func TestCronSeriesPrev(t *testing.T) {
	t.Run("Should return the last matching time for a range of expressions", func(t *testing.T) {
		layout := "2006-01-02 15:04:05"
		cases := []struct {
			name   string
			expr   string
			before string
			want   string
		}{
			{
				name:   "every 5 minutes",
				expr:   "5 * * * *",
				before: "2025-08-15 12:09:00",
				want:   "2025-08-15 12:05:00",
			},
			{
				name:   "exact match excludes itself",
				expr:   "5 * * * *",
				before: "2025-08-15 12:05:00",
				want:   "2025-08-15 11:05:00",
			},
			{
				name:   "walks back into the previous month",
				expr:   "15 14 1 * *",
				before: "2025-09-15 12:01:00",
				want:   "2025-09-01 14:15:00",
			},
			{
				// Friday the 22nd, which is the most recent match
				// before the 23rd, ahead of the 20th.
				name:   "dom and dow restricted, most recent wins",
				expr:   "0 0 20 * 5",
				before: "2025-08-23 00:00:00",
				want:   "2025-08-22 00:00:00",
			},
		}

		for _, c := range cases {
			series, err := NewCronSeries(c.expr)
			if err != nil {
				t.Fatalf("%s: failed to create cron series: %v", c.name, err)
			}
			before := mustParseTime(t, layout, c.before)
			got := series.Prev(before)
			want := mustParseTime(t, layout, c.want)
			assert.Equal(t, want, got, c.name)
		}
	})
}

func TestCronSeriesPrevNoMatch(t *testing.T) {
	t.Run("Should return the zero time when no match exists in the search window", func(t *testing.T) {
		series, err := NewCronSeries("0 0 31 2 *")
		if err != nil {
			t.Fatalf("failed to create cron series: %v", err)
		}
		before := mustParseTime(t, "2006-01-02 15:04:05", "2025-08-13 00:00:00")
		assert.True(t, series.Prev(before).IsZero())
	})
}

func TestCronSeriesUntilNext(t *testing.T) {
	t.Run("Should return the duration until the next match", func(t *testing.T) {
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
	})

	t.Run("Should return an error when no match exists", func(t *testing.T) {
		series, err := NewCronSeries("0 0 31 2 *")
		if err != nil {
			t.Fatalf("failed to create cron series: %v", err)
		}
		from := mustParseTime(t, "2006-01-02 15:04:05", "2025-08-13 00:00:00")
		_, err = series.UntilNext(from)
		assert.ErrorIs(t, err, ErrNoMatch)
	})
}

func TestCronSeriesSincePrev(t *testing.T) {
	t.Run("Should return the duration since the last match", func(t *testing.T) {
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
	})

	t.Run("Should return an error when no match exists", func(t *testing.T) {
		series, err := NewCronSeries("0 0 31 2 *")
		if err != nil {
			t.Fatalf("failed to create cron series: %v", err)
		}
		from := mustParseTime(t, "2006-01-02 15:04:05", "2025-08-13 00:00:00")
		_, err = series.SincePrev(from)
		assert.ErrorIs(t, err, ErrNoMatch)
	})
}

func TestCronSeriesMatch(t *testing.T) {
	t.Run("Should match or reject a range of times regardless of seconds", func(t *testing.T) {
		layout := "2006-01-02 15:04:05"
		cases := []struct {
			name string
			expr string
			t    string
			want bool
		}{
			{
				name: "matches on the scheduled minute",
				expr: "5 * * * *",
				t:    "2025-08-15 12:05:00",
				want: true,
			},
			{
				name: "matches regardless of seconds",
				expr: "5 * * * *",
				t:    "2025-08-15 12:05:42",
				want: true,
			},
			{
				name: "rejects a different minute",
				expr: "5 * * * *",
				t:    "2025-08-15 12:06:00",
				want: false,
			},
			{
				name: "matches with dom/dow union",
				expr: "0 0 20 * 5",
				t:    "2025-08-22 00:00:00", // Friday the 22nd
				want: true,
			},
		}

		for _, c := range cases {
			series, err := NewCronSeries(c.expr)
			if err != nil {
				t.Fatalf("%s: failed to create cron series: %v", c.name, err)
			}
			got := series.Match(mustParseTime(t, layout, c.t))
			assert.Equal(t, c.want, got, c.name)
		}
	})
}

func TestCronSeriesMatchRange(t *testing.T) {
	t.Run("Should report whether an occurrence falls within a range", func(t *testing.T) {
		layout := "2006-01-02 15:04:05"
		cases := []struct {
			name string
			expr string
			from string
			to   string
			want bool
		}{
			{
				name: "from itself is a match",
				expr: "5 * * * *",
				from: "2025-08-15 12:05:00",
				to:   "2025-08-15 12:05:00",
				want: true,
			},
			{
				name: "an occurrence falls inside the range",
				expr: "5 * * * *",
				from: "2025-08-15 12:01:00",
				to:   "2025-08-15 12:10:00",
				want: true,
			},
			{
				name: "no occurrence falls inside the range",
				expr: "5 * * * *",
				from: "2025-08-15 12:06:00",
				to:   "2025-08-15 12:59:00",
				want: false,
			},
			{
				name: "to before from is always false",
				expr: "5 * * * *",
				from: "2025-08-15 12:10:00",
				to:   "2025-08-15 12:01:00",
				want: false,
			},
		}

		for _, c := range cases {
			series, err := NewCronSeries(c.expr)
			if err != nil {
				t.Fatalf("%s: failed to create cron series: %v", c.name, err)
			}
			from := mustParseTime(t, layout, c.from)
			to := mustParseTime(t, layout, c.to)
			got := series.MatchRange(from, to)
			assert.Equal(t, c.want, got, c.name)
		}
	})
}

func TestNewCronSeriesInvalidExpr(t *testing.T) {
	t.Run("Should reject a range of invalid expressions", func(t *testing.T) {
		cases := []struct {
			name string
			expr string
		}{
			{name: "wrong field count", expr: "* * * *"},
			{name: "bad step value", expr: "*/x * * * *"},
			{name: "bad range", expr: "60-10 * * * *"},
			{name: "bad value", expr: "99 * * * *"},
			{name: "bad month/weekday name", expr: "0 0 * bogus *"},
		}

		for _, c := range cases {
			_, err := NewCronSeries(c.expr)
			assert.ErrorIs(t, err, ErrInvalidExpr, c.name)
		}
	})
}

func TestIsValid(t *testing.T) {
	t.Run("Should accept a valid expression and reject an invalid one", func(t *testing.T) {
		assert.True(t, IsValid("5 * * * *"))
		assert.False(t, IsValid("not a cron expression"))
	})
}

func TestCronSeriesAliases(t *testing.T) {
	t.Run("Should resolve each alias to its equivalent expression", func(t *testing.T) {
		layout := "2006-01-02 15:04:05"
		cases := []struct {
			name  string
			alias string
			after string
			want  string
		}{
			{
				name:  "@yearly",
				alias: "@yearly",
				after: "2025-06-01 00:00:00",
				want:  "2026-01-01 00:00:00",
			},
			{
				name:  "@annually",
				alias: "@annually",
				after: "2025-06-01 00:00:00",
				want:  "2026-01-01 00:00:00",
			},
			{
				name:  "@monthly",
				alias: "@monthly",
				after: "2025-08-15 00:00:00",
				want:  "2025-09-01 00:00:00",
			},
			{
				name:  "@weekly",
				alias: "@weekly",
				after: "2025-08-13 00:00:00", // Wednesday
				want:  "2025-08-17 00:00:00", // Sunday
			},
			{
				name:  "@daily",
				alias: "@daily",
				after: "2025-08-13 12:00:00",
				want:  "2025-08-14 00:00:00",
			},
			{
				name:  "@midnight",
				alias: "@midnight",
				after: "2025-08-13 12:00:00",
				want:  "2025-08-14 00:00:00",
			},
			{
				name:  "@hourly",
				alias: "@hourly",
				after: "2025-08-13 12:30:00",
				want:  "2025-08-13 13:00:00",
			},
			{
				name:  "case insensitive",
				alias: "@HOURLY",
				after: "2025-08-13 12:30:00",
				want:  "2025-08-13 13:00:00",
			},
		}

		for _, c := range cases {
			series, err := NewCronSeries(c.alias)
			if err != nil {
				t.Fatalf("%s: failed to create cron series: %v", c.name, err)
			}
			after := mustParseTime(t, layout, c.after)
			got := series.Next(after)
			want := mustParseTime(t, layout, c.want)
			assert.Equal(t, want, got, c.name)
		}
	})
}

func TestCronSeriesMonthAndWeekdayNames(t *testing.T) {
	t.Run("Should accept month and weekday names in place of numbers", func(t *testing.T) {
		layout := "2006-01-02 15:04:05"
		cases := []struct {
			name  string
			expr  string
			after string
			want  string
		}{
			{
				name:  "month name",
				expr:  "0 0 1 Jan *",
				after: "2025-06-01 00:00:00",
				want:  "2026-01-01 00:00:00",
			},
			{
				name:  "month range by name",
				expr:  "0 0 1 jan-mar *",
				after: "2025-04-01 00:00:00",
				want:  "2026-01-01 00:00:00",
			},
			{
				name:  "weekday name",
				expr:  "0 0 * * MON",
				after: "2025-08-13 00:00:00", // Wednesday
				want:  "2025-08-18 00:00:00", // Monday
			},
		}

		for _, c := range cases {
			series, err := NewCronSeries(c.expr)
			if err != nil {
				t.Fatalf("%s: failed to create cron series: %v", c.name, err)
			}
			after := mustParseTime(t, layout, c.after)
			got := series.Next(after)
			want := mustParseTime(t, layout, c.want)
			assert.Equal(t, want, got, c.name)
		}
	})
}
