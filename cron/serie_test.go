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
func TestCronSerie(t *testing.T) {
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
			name:  "At the 20th day of the month",
			expr:  "0 0 20 * *",
			after: "2025-08-13 00:00:00",
			want:  "2025-08-17 00:00:00", // Next Sunday
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			serie, err := NewCronSerie(c.expr)
			if err != nil {
				t.Fatalf("failed to create cron serie: %v", err)
			}
			after := mustParseTime(t, "2006-01-02 15:04:05", c.after)
			got := serie.Next(after)
			want := mustParseTime(t, layout, c.want)
			assert.Equal(t, want, got)
		})
	}
}
