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

	// multiple presets combine, deduped across sets
	got, err = resolveDestinations("", "caribbean,europe", "ORF")
	if err != nil {
		t.Fatal(err)
	}
	if want := len(destinationPresets["caribbean"]) + len(destinationPresets["europe"]); len(got) != want {
		t.Errorf("combined preset count = %d, want %d", len(got), want)
	}
	if !contains(got, "SJU") || !contains(got, "LHR") {
		t.Errorf("combined preset missing expected members: %v", got)
	}

	// errors
	if _, err := resolveDestinations("", "", "ORF"); err == nil {
		t.Error("empty destinations should error")
	}
	if _, err := resolveDestinations("", "no-such-preset", "ORF"); err == nil {
		t.Error("unknown preset should error")
	}
	if _, err := resolveDestinations("", "caribbean,bogus", "ORF"); err == nil {
		t.Error("one bad preset in a list should error")
	}
	if _, err := resolveDestinations("ORF", "", "ORF"); err == nil {
		t.Error("destinations that are all the origin should error")
	}
}

func TestDestinationPresetsWellFormed(t *testing.T) {
	for name, codes := range destinationPresets {
		if len(codes) == 0 {
			t.Errorf("preset %q is empty", name)
		}
		seen := map[string]bool{}
		for _, code := range codes {
			if !isAirportCode(code) {
				t.Errorf("preset %q has non-IATA entry %q", name, code)
			}
			if seen[code] {
				t.Errorf("preset %q has duplicate %q", name, code)
			}
			seen[code] = true
		}
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

func TestMedianOfferPrice(t *testing.T) {
	// odd count (5 priced), ignores the zero-priced entry
	if got := medianOfferPrice([]flights.Offer{{Price: 0}, {Price: 300}, {Price: 100}, {Price: 200}, {Price: 500}, {Price: 400}}); got != 300 {
		t.Errorf("odd median = %v, want 300", got)
	}
	// even count -> average of the two middle values
	if got := medianOfferPrice([]flights.Offer{{Price: 100}, {Price: 200}, {Price: 300}, {Price: 500}}); got != 250 {
		t.Errorf("even median = %v, want 250", got)
	}
	// a couple of expensive dates must not drag the baseline like a mean would
	if got := medianOfferPrice([]flights.Offer{{Price: 100}, {Price: 110}, {Price: 120}, {Price: 900}, {Price: 950}}); got != 120 {
		t.Errorf("skewed median = %v, want 120 (mean would be ~436)", got)
	}
	if got := medianOfferPrice(nil); got != 0 {
		t.Errorf("empty median = %v, want 0", got)
	}
}

func TestRankDeals(t *testing.T) {
	base := func() []deal {
		return []deal{
			{Dest: "ATL", Price: 80, Discount: 20},
			{Dest: "MCO", Price: 123, Discount: 50},
			{Dest: "LAS", Price: 227, Discount: 5},
		}
	}

	// sort by price (cheapest first)
	got, err := rankDeals(base(), "price", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Dest != "ATL" || got[2].Dest != "LAS" {
		t.Errorf("price sort = %v", dests(got))
	}

	// sort by deal (biggest discount first)
	got, _ = rankDeals(base(), "deal", 0, 0)
	if got[0].Dest != "MCO" || got[1].Dest != "ATL" || got[2].Dest != "LAS" {
		t.Errorf("deal sort = %v", dests(got))
	}

	// min-discount filter drops shallow deals
	got, _ = rankDeals(base(), "deal", 20, 0)
	if len(got) != 2 || got[0].Dest != "MCO" || got[1].Dest != "ATL" {
		t.Errorf("min-discount filter = %v", dests(got))
	}

	// limit trims after sorting
	got, _ = rankDeals(base(), "deal", 0, 1)
	if len(got) != 1 || got[0].Dest != "MCO" {
		t.Errorf("limit = %v", dests(got))
	}

	if _, err := rankDeals(base(), "bogus", 0, 0); err == nil {
		t.Error("invalid sort should error")
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func dests(ds []deal) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.Dest
	}
	return out
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
		{Dest: "ATL", Price: 80, Typical: 160, Discount: 50, Depart: "2026-11-03", Return: "2026-11-07", Currency: "USD"},
		{Dest: "MCO", Price: 123, Typical: 150, Discount: 18, Depart: "2026-11-03", Return: "2026-11-07", Currency: "USD"},
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
		"TYPICAL", "DEAL", "ATL", "80.00 USD", "50%", "MCO", "1 destination(s) returned no offers: BOS",
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
			Dest     string  `json:"dest"`
			Price    float64 `json:"price"`
			Typical  float64 `json:"typical"`
			Discount float64 `json:"discount"`
			Depart   string  `json:"depart"`
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
	if j.Deals[0].Dest != "ATL" || j.Deals[0].Price != 80 || j.Deals[0].Typical != 160 || j.Deals[0].Discount != 50 {
		t.Errorf("deal[0] = %+v", j.Deals[0])
	}
	if len(j.Failures) != 1 || j.Failures[0].Dest != "BOS" {
		t.Errorf("failures = %+v", j.Failures)
	}
}
