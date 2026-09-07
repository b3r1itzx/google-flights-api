package main

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/b3r1itzx/google-flights-api/flights"
)

func TestResolveDestinations(t *testing.T) {
	// comma list, origin removed, deduped, case-insensitive
	got, err := resolveDestinations("MCO, ATL ,LAS,mco,ORF", "", "ORF")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"MCO", "ATL", "LAS"}; !equalSlices(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	// preset expands and appends
	got, err = resolveDestinations("MCO", "us-major", "ORF")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 20 || got[0] != "MCO" {
		t.Errorf("preset expansion looks wrong: %v", got)
	}
	// origin (ORF not in preset, but JFK is) removed when it is the origin
	got, _ = resolveDestinations("", "us-major", "JFK")
	for _, d := range got {
		if d == "JFK" {
			t.Error("origin JFK should be removed from preset")
		}
	}

	// errors
	if _, err := resolveDestinations("", "", "ORF"); err == nil {
		t.Error("empty destinations should error")
	}
	if _, err := resolveDestinations("", "no-such-preset", "ORF"); err == nil {
		t.Error("unknown preset should error")
	}
	if _, err := resolveDestinations("ORF", "", "ORF"); err == nil {
		t.Error("destinations that are all the origin should error")
	}
}

func TestCheapestOffer(t *testing.T) {
	offers := []flights.Offer{
		{Price: 0}, {Price: 250}, {Price: 80}, {Price: 300},
	}
	best, ok := cheapestOffer(offers)
	if !ok || best.Price != 80 {
		t.Errorf("cheapest = %v, %v; want 80", best.Price, ok)
	}
	if _, ok := cheapestOffer([]flights.Offer{{Price: 0}}); ok {
		t.Error("all-zero offers should report not found")
	}
	if _, ok := cheapestOffer(nil); ok {
		t.Error("no offers should report not found")
	}
}

func TestRunDealsFlagValidation(t *testing.T) {
	cases := []struct {
		name    string
		argv    []string
		wantErr string
	}{
		{"missing from", []string{"--preset", "us-major", "--start", "2026-11-01", "--end", "2026-11-30"}, "--from is required"},
		{"missing dates", []string{"--from", "ORF", "--preset", "us-major"}, "--start and --end are required"},
		{"no destinations", []string{"--from", "ORF", "--start", "2026-11-01", "--end", "2026-11-30"}, "no destinations"},
		{"bad preset", []string{"--from", "ORF", "--preset", "nope", "--start", "2026-11-01", "--end", "2026-11-30"}, "unknown --preset"},
		{"bad date", []string{"--from", "ORF", "--to", "MCO", "--start", "nope", "--end", "2026-11-30"}, "--start"},
		{"bad currency", []string{"--from", "ORF", "--to", "MCO", "--start", "2026-11-01", "--end", "2026-11-30", "--currency", "DOLLARS"}, "invalid --currency"},
	}
	for _, c := range cases {
		err := runDeals(c.argv, io.Discard)
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want containing %q", c.name, err, c.wantErr)
		}
	}
}

func testDeals() []deal {
	return []deal{
		{Dest: "ATL", Price: 80, Depart: "2026-11-03", Return: "2026-11-07", Currency: "USD"},
		{Dest: "MCO", Price: 123, Depart: "2026-11-03", Return: "2026-11-07", Currency: "USD"},
	}
}

func TestWriteDealsText(t *testing.T) {
	var buf bytes.Buffer
	c := commonOpts{from: "ORF"}
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)
	writeDealsText(&buf, c, start, end, 4, 5, 2, testDeals(), map[string]string{"BOS": "no offers with a price"})
	out := buf.String()
	for _, want := range []string{
		"ORF -> 5 destinations", "2026-11-01..2026-11-30", "4-day trip", "2 with offers",
		"ATL", "80.00 USD", "MCO", "1 destination(s) returned no offers: BOS",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q:\n%s", want, out)
		}
	}
}

func TestWriteDealsJSON(t *testing.T) {
	var buf bytes.Buffer
	c := commonOpts{from: "ORF", currency: "USD", lang: "en", class: "economy", stops: "any", tripType: "round-trip"}
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)
	if err := writeDealsJSON(&buf, c, start, end, 4, 5, 2, testDeals(), map[string]string{"BOS": "no offers with a price"}); err != nil {
		t.Fatal(err)
	}
	var j struct {
		Type  string `json:"type"`
		Count int    `json:"count"`
		Query struct {
			From       string `json:"from"`
			RangeStart string `json:"range_start"`
			Duration   int    `json:"duration_days"`
		} `json:"query"`
		Deals []struct {
			Dest   string  `json:"dest"`
			Price  float64 `json:"price"`
			Depart string  `json:"depart"`
		} `json:"deals"`
		Failures []struct {
			Dest   string `json:"dest"`
			Reason string `json:"reason"`
		} `json:"failures"`
	}
	if err := json.Unmarshal(buf.Bytes(), &j); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if j.Type != "deals" || j.Count != 2 {
		t.Errorf("envelope = %+v", j)
	}
	if j.Query.From != "ORF" || j.Query.RangeStart != "2026-11-01" || j.Query.Duration != 4 {
		t.Errorf("query = %+v", j.Query)
	}
	if j.Deals[0].Dest != "ATL" || j.Deals[0].Price != 80 {
		t.Errorf("deal[0] = %+v", j.Deals[0])
	}
	if len(j.Failures) != 1 || j.Failures[0].Dest != "BOS" {
		t.Errorf("failures = %+v", j.Failures)
	}
}
