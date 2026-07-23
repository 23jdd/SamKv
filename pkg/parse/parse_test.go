package parse

import (
	"errors"
	"testing"
	"time"
)

func TestParseQueryFormat(t *testing.T) {
	query, err := ParseQueryFormat(`"upstream connection failed"{app=nginx,level="ERROR",status=500}[5m] offset 1h30m`)
	if err != nil {
		t.Fatalf("ParseQueryFormat() error = %v", err)
	}
	if query.Query != "upstream connection failed" {
		t.Fatalf("query=%q, want %q", query.Query, "upstream connection failed")
	}
	wantLabels := []LabelMatcher{
		{Name: "app", Value: "nginx"},
		{Name: "level", Value: "ERROR"},
		{Name: "status", Value: "500"},
	}
	if len(query.Labels) != len(wantLabels) {
		t.Fatalf("labels=%#v, want %#v", query.Labels, wantLabels)
	}
	for i := range wantLabels {
		if query.Labels[i] != wantLabels[i] {
			t.Fatalf("label %d=%#v, want %#v", i, query.Labels[i], wantLabels[i])
		}
	}
	if query.Range.Value() != 5*time.Minute {
		t.Fatalf("range=%s, want 5m", query.Range)
	}
	if query.Offset.Value() != 90*time.Minute {
		t.Fatalf("offset=%s, want 1h30m", query.Offset)
	}
}

func TestParseQueryFormatAllowsWhitespaceEmptyLabelsAndNoOffset(t *testing.T) {
	query, err := ParseQueryFormat("  error { } [ 30s ]  ")
	if err != nil {
		t.Fatalf("ParseQueryFormat() error = %v", err)
	}
	if query.Query != "error" {
		t.Fatalf("query=%q, want error", query.Query)
	}
	if len(query.Labels) != 0 {
		t.Fatalf("labels=%#v, want empty", query.Labels)
	}
	if query.Range.Value() != 30*time.Second || query.Offset.Value() != 0 {
		t.Fatalf("range=%s offset=%s", query.Range, query.Offset)
	}
}

func TestParseQueryFormatUnquotesLabelValue(t *testing.T) {
	query, err := ParseQueryFormat(`"connection refused"{message="api\nready",instance="server,1"}[1.5s]`)
	if err != nil {
		t.Fatalf("ParseQueryFormat() error = %v", err)
	}
	if query.Query != "connection refused" {
		t.Fatalf("query=%q, want %q", query.Query, "connection refused")
	}
	if query.Labels[0].Value != "api\nready" {
		t.Fatalf("message=%q", query.Labels[0].Value)
	}
	if query.Labels[1].Value != "server,1" {
		t.Fatalf("instance=%q", query.Labels[1].Value)
	}
	if query.Range.Value() != 1500*time.Millisecond {
		t.Fatalf("range=%s, want 1.5s", query.Range)
	}
}

func TestQueryFormatTimeRange(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	query, err := ParseQueryFormat(`api-error{app=api}[15m] offset 1h`)
	if err != nil {
		t.Fatal(err)
	}

	start, end := query.TimeRange(now)
	if want := now.Add(-75 * time.Minute); !start.Equal(want) {
		t.Fatalf("start=%s, want %s", start, want)
	}
	if want := now.Add(-time.Hour); !end.Equal(want) {
		t.Fatalf("end=%s, want %s", end, want)
	}
}

func TestParseQueryFormatRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "empty", input: ""},
		{name: "missing matcher", input: `{app=api}[5m]`},
		{name: "empty matcher", input: `""{app=api}[5m]`},
		{name: "missing range", input: `error{app=api}`},
		{name: "unsupported day duration", input: `error{app=api}[1d]`},
		{name: "zero range", input: `error{app=api}[0s]`},
		{name: "duplicate label", input: `error{app=api,app=worker}[5m]`},
		{name: "missing value", input: `error{app=}[5m]`},
		{name: "trailing input", input: `error{app=api}[5m] unexpected`},
		{name: "negative offset", input: `error{app=api}[5m] offset -1m`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseQueryFormat(test.input)
			if !errors.Is(err, ErrInvalidQueryFormat) {
				t.Fatalf("error=%v, want ErrInvalidQueryFormat", err)
			}
		})
	}
}
