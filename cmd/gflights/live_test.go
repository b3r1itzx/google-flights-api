package main

// End-to-end tests that run each subcommand in-process against the live
// Google Flights/Hotels backends through the headless-browser session, and
// verify the machine-readable output still carries real data. These are the
// canary tests: when Google changes a request format, response schema, or
// starts gating a new endpoint, these fail first.
//
// Skipped in -short mode. They need a Chrome/Chromium binary (see
// flights.NewBrowserSession; override with CHROME_BIN).

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"
	"time"
)

func runJSON(t *testing.T, run func([]string, io.Writer) error, argv []string, out interface{}) {
	t.Helper()
	var buf bytes.Buffer
	if err := run(append(argv, "--json"), &buf); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(buf.Bytes(), out); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, buf.String())
	}
}

func day(t time.Time) string { return t.Format("2006-01-02") }

func TestLiveOffers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live CLI test in -short mode")
	}
	depart := time.Now().AddDate(0, 2, 0)
	var out struct {
		Type       string `json:"type"`
		Count      int    `json:"count"`
		URL        string `json:"url"`
		PriceRange *struct {
			Low float64 `json:"low"`
		} `json:"price_range"`
		Offers []struct {
			Price           float64 `json:"price"`
			Currency        string  `json:"currency"`
			DurationMinutes int     `json:"duration_minutes"`
			Flights         []struct {
				Airline      string `json:"airline"`
				FlightNumber string `json:"flight_number"`
				From         string `json:"from"`
				Departure    string `json:"departure"`
			} `json:"flights"`
		} `json:"offers"`
	}
	runJSON(t, runOffers, []string{
		"--from", "JFK", "--to", "LHR",
		"--depart", day(depart), "--return", day(depart.AddDate(0, 0, 7)),
		"--limit", "5",
	}, &out)

	if out.Type != "offers" || out.Count == 0 {
		t.Fatalf("expected offers, got type=%q count=%d", out.Type, out.Count)
	}
	if out.URL == "" {
		t.Error("expected a booking URL")
	}
	for i, o := range out.Offers {
		if o.Price <= 0 {
			t.Errorf("offer %d has no price", i)
		}
		if o.Currency != "USD" {
			t.Errorf("offer %d currency = %q, want USD", i, o.Currency)
		}
		if o.DurationMinutes <= 0 {
			t.Errorf("offer %d has no duration", i)
		}
		if len(o.Flights) == 0 {
			t.Fatalf("offer %d has no flights", i)
		}
		f := o.Flights[0]
		if f.Airline == "" || f.FlightNumber == "" || f.From == "" {
			t.Errorf("offer %d flight incomplete: %+v", i, f)
		}
		if _, err := time.Parse(time.RFC3339, f.Departure); err != nil {
			t.Errorf("offer %d departure not RFC3339: %q", i, f.Departure)
		}
	}
}

func TestLivePriceGraph(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live CLI test in -short mode")
	}
	start := time.Now().AddDate(0, 1, 0)
	end := start.AddDate(0, 1, 0)
	var out struct {
		Type   string `json:"type"`
		Count  int    `json:"count"`
		Offers []struct {
			Depart string  `json:"depart"`
			Return string  `json:"return"`
			Price  float64 `json:"price"`
		} `json:"offers"`
	}
	runJSON(t, runPriceGraph, []string{
		"--from", "JFK", "--to", "LHR",
		"--start", day(start), "--end", day(end), "--duration", "7",
	}, &out)

	if out.Type != "pricegraph" || out.Count == 0 {
		t.Fatalf("expected price graph offers, got type=%q count=%d", out.Type, out.Count)
	}
	// the graph should cover most of the requested range
	if out.Count < 20 {
		t.Errorf("expected ~30 dates, got %d", out.Count)
	}
	priced := 0
	for i, o := range out.Offers {
		d, err := time.Parse("2006-01-02", o.Depart)
		if err != nil {
			t.Fatalf("offer %d bad depart %q", i, o.Depart)
		}
		r, err := time.Parse("2006-01-02", o.Return)
		if err != nil {
			t.Fatalf("offer %d bad return %q", i, o.Return)
		}
		if want := d.AddDate(0, 0, 7); !r.Equal(want) {
			t.Errorf("offer %d return %s, want %s (7-day trip)", i, o.Return, day(want))
		}
		if o.Price > 0 {
			priced++
		}
	}
	if priced == 0 {
		t.Error("no offer has a price")
	}
}

func TestLiveHotels(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live CLI test in -short mode")
	}
	ci := time.Now().AddDate(0, 1, 0)
	var out struct {
		Type   string `json:"type"`
		Count  int    `json:"count"`
		Hotels []struct {
			Name     string  `json:"name"`
			Price    float64 `json:"price"`
			Currency string  `json:"currency"`
		} `json:"hotels"`
	}
	runJSON(t, runHotels, []string{
		"--location", "New York",
		"--checkin", day(ci), "--checkout", day(ci.AddDate(0, 0, 3)),
	}, &out)

	if out.Type != "hotels" || out.Count == 0 {
		t.Fatalf("expected hotels, got type=%q count=%d", out.Type, out.Count)
	}
	priced := 0
	for i, h := range out.Hotels {
		if h.Name == "" {
			t.Errorf("hotel %d has no name", i)
		}
		if h.Currency != "USD" {
			t.Errorf("hotel %d currency = %q, want USD", i, h.Currency)
		}
		if h.Price > 0 {
			priced++
		}
	}
	if priced == 0 {
		t.Error("no hotel has a price — the AtySUc capture or price parsing likely broke")
	}
}

func TestLiveHotelPriceGraph(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live CLI test in -short mode")
	}
	start := time.Now().AddDate(0, 1, 0)
	var out struct {
		Type   string `json:"type"`
		Count  int    `json:"count"`
		Offers []struct {
			CheckIn string `json:"checkin"`
			Hotel   struct {
				Name  string  `json:"name"`
				Price float64 `json:"price"`
			} `json:"hotel"`
		} `json:"offers"`
	}
	runJSON(t, runHotelPriceGraph, []string{
		"--location", "New York",
		"--start", day(start), "--end", day(start.AddDate(0, 0, 3)),
		"--nights", "2", "--step", "3",
	}, &out)

	if out.Type != "hotel-pricegraph" {
		t.Fatalf("type = %q", out.Type)
	}
	// 2 sampled dates; expect at least one to have a priced hotel
	if out.Count == 0 {
		t.Fatal("no date offers — hotel search or price parsing likely broke")
	}
	for i, o := range out.Offers {
		if o.Hotel.Name == "" || o.Hotel.Price <= 0 {
			t.Errorf("offer %d incomplete: %+v", i, o)
		}
		if _, err := time.Parse("2006-01-02", o.CheckIn); err != nil {
			t.Errorf("offer %d bad checkin %q", i, o.CheckIn)
		}
	}
}

// TestLiveTextOutput smoke-tests the human-readable path of one subcommand so
// a formatting regression there doesn't go unnoticed (the other live tests
// all use --json).
func TestLiveTextOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live CLI test in -short mode")
	}
	depart := time.Now().AddDate(0, 2, 0)
	var buf bytes.Buffer
	err := runOffers([]string{
		"--from", "JFK", "--to", "LHR",
		"--depart", day(depart), "--return", day(depart.AddDate(0, 0, 7)),
		"--limit", "3",
	}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"JFK -> LHR", "offers", "USD", "#1"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("text output missing %q:\n%s", want, out)
		}
	}
}
