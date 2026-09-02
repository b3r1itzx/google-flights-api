package main

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/b3r1itzx/google-flights-api/flights"
	"golang.org/x/text/currency"
	"golang.org/x/text/language"
)

// --- flag / argument parsing ---

func TestSplitLocations(t *testing.T) {
	cities, airports := splitLocations("JFK, New York , LHR,Rome,")
	if want := []string{"New York", "Rome"}; !equalSlices(cities, want) {
		t.Errorf("cities = %v, want %v", cities, want)
	}
	if want := []string{"JFK", "LHR"}; !equalSlices(airports, want) {
		t.Errorf("airports = %v, want %v", airports, want)
	}
}

func TestIsAirportCode(t *testing.T) {
	for in, want := range map[string]bool{
		"JFK": true, "LHR": true,
		"jfk": false, "NYC1": false, "NY": false, "Rome": false,
	} {
		if got := isAirportCode(in); got != want {
			t.Errorf("isAirportCode(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseClass(t *testing.T) {
	cases := map[string]flights.Class{
		"economy": flights.Economy, "premium-economy": flights.PremiumEconomy,
		"premium": flights.PremiumEconomy, "business": flights.Business,
		"first": flights.First, "premium_economy": flights.PremiumEconomy,
	}
	for in, want := range cases {
		got, err := parseClass(in)
		if err != nil || got != want {
			t.Errorf("parseClass(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := parseClass("luxury"); err == nil {
		t.Error("parseClass(luxury) should fail")
	}
}

func TestParseStops(t *testing.T) {
	cases := map[string]flights.Stops{
		"any": flights.AnyStops, "": flights.AnyStops,
		"nonstop": flights.Nonstop, "0": flights.Nonstop,
		"stop1": flights.Stop1, "1": flights.Stop1,
		"stop2": flights.Stop2, "2": flights.Stop2,
	}
	for in, want := range cases {
		got, err := parseStops(in)
		if err != nil || got != want {
			t.Errorf("parseStops(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := parseStops("3"); err == nil {
		t.Error("parseStops(3) should fail")
	}
}

func TestParseTripType(t *testing.T) {
	for _, in := range []string{"round-trip", "round", "rt", "round_trip"} {
		if got, err := parseTripType(in); err != nil || got != flights.RoundTrip {
			t.Errorf("parseTripType(%q) = %v, %v; want RoundTrip", in, got, err)
		}
	}
	for _, in := range []string{"one-way", "oneway", "ow"} {
		if got, err := parseTripType(in); err != nil || got != flights.OneWay {
			t.Errorf("parseTripType(%q) = %v, %v; want OneWay", in, got, err)
		}
	}
	if _, err := parseTripType("multi-city"); err == nil {
		t.Error("parseTripType(multi-city) should fail")
	}
}

func TestParseDate(t *testing.T) {
	d, err := parseDate("2026-07-06", "depart")
	if err != nil {
		t.Fatal(err)
	}
	if d.Year() != 2026 || d.Month() != 7 || d.Day() != 6 {
		t.Errorf("parseDate = %v", d)
	}
	if _, err := parseDate("07/06/2026", "depart"); err == nil ||
		!strings.Contains(err.Error(), "--depart") {
		t.Errorf("bad date should fail mentioning the flag, got: %v", err)
	}
}

func TestCommonOptsResolve(t *testing.T) {
	c := commonOpts{
		from: "JFK,New York", to: "Rome", adults: 2, children: 1,
		class: "business", stops: "nonstop", currency: "eur", lang: "en",
		tripType: "one-way",
	}
	opts, sd, err := c.resolve()
	if err != nil {
		t.Fatal(err)
	}
	if opts.Travelers.Adults != 2 || opts.Travelers.Children != 1 {
		t.Errorf("travelers = %+v", opts.Travelers)
	}
	if opts.Class != flights.Business || opts.Stops != flights.Nonstop || opts.TripType != flights.OneWay {
		t.Errorf("opts = %+v", opts)
	}
	if opts.Currency != currency.EUR {
		t.Errorf("currency = %v, want EUR", opts.Currency)
	}
	if !equalSlices(sd.srcAirports, []string{"JFK"}) || !equalSlices(sd.srcCities, []string{"New York"}) {
		t.Errorf("src = %+v", sd)
	}
	if !equalSlices(sd.dstCities, []string{"Rome"}) || len(sd.dstAirports) != 0 {
		t.Errorf("dst = %+v", sd)
	}

	for name, bad := range map[string]commonOpts{
		"missing from":  {to: "Rome", class: "economy", stops: "any", currency: "USD", lang: "en", tripType: "rt"},
		"bad class":     {from: "JFK", to: "LHR", class: "luxury", stops: "any", currency: "USD", lang: "en", tripType: "rt"},
		"bad stops":     {from: "JFK", to: "LHR", class: "economy", stops: "5", currency: "USD", lang: "en", tripType: "rt"},
		"bad currency":  {from: "JFK", to: "LHR", class: "economy", stops: "any", currency: "DOLLARS", lang: "en", tripType: "rt"},
		"bad lang":      {from: "JFK", to: "LHR", class: "economy", stops: "any", currency: "USD", lang: "no-such-lang!", tripType: "rt"},
		"bad trip type": {from: "JFK", to: "LHR", class: "economy", stops: "any", currency: "USD", lang: "en", tripType: "circular"},
	} {
		if _, _, err := bad.resolve(); err == nil {
			t.Errorf("%s: resolve should fail", name)
		}
	}
}

func TestHotelCommonOptsResolve(t *testing.T) {
	c := hotelCommonOpts{location: "New York", minStars: 4, maxStars: 5, currency: "usd", lang: "en"}
	opts, err := c.resolve()
	if err != nil {
		t.Fatal(err)
	}
	if opts.MinStars != 4 || opts.MaxStars != 5 || opts.Currency != currency.USD {
		t.Errorf("opts = %+v", opts)
	}

	for name, bad := range map[string]hotelCommonOpts{
		"missing location": {currency: "USD", lang: "en"},
		"bad stars":        {location: "NY", minStars: 6, currency: "USD", lang: "en"},
		"bad currency":     {location: "NY", currency: "DOLLARS", lang: "en"},
		"bad lang":         {location: "NY", currency: "USD", lang: "no-such-lang!"},
	} {
		if _, err := bad.resolve(); err == nil {
			t.Errorf("%s: resolve should fail", name)
		}
	}
}

// --- pre-network argument validation of the run functions ---

func TestRunFlagValidation(t *testing.T) {
	cases := []struct {
		name    string
		run     func([]string, io.Writer) error
		argv    []string
		wantErr string
	}{
		{"pricegraph missing dates", runPriceGraph, []string{"--from", "JFK", "--to", "LHR"}, "--start and --end are required"},
		{"pricegraph bad date", runPriceGraph, []string{"--from", "JFK", "--to", "LHR", "--start", "bogus", "--end", "2026-12-01"}, "--start"},
		{"pricegraph missing route", runPriceGraph, []string{"--start", "2026-11-01", "--end", "2026-12-01"}, "--from and --to are required"},
		{"offers missing depart", runOffers, []string{"--from", "JFK", "--to", "LHR"}, "--depart is required"},
		{"offers missing return", runOffers, []string{"--from", "JFK", "--to", "LHR", "--depart", "2026-11-01"}, "--return is required"},
		{"offers bad date", runOffers, []string{"--from", "JFK", "--to", "LHR", "--depart", "01-11-2026"}, "--depart"},
		{"hotels missing dates", runHotels, []string{"--location", "New York"}, "--checkin and --checkout are required"},
		{"hotels missing location", runHotels, []string{"--checkin", "2026-11-01", "--checkout", "2026-11-05"}, "--location is required"},
		{"hotel-pricegraph missing dates", runHotelPriceGraph, []string{"--location", "New York"}, "--start and --end are required"},
	}
	for _, c := range cases {
		err := c.run(c.argv, io.Discard)
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want containing %q", c.name, err, c.wantErr)
		}
	}
}

// --- output formatting (fixtures, no network) ---

func fixtureOffer() flights.FullOffer {
	dep := time.Date(2026, 7, 6, 22, 5, 0, 0, time.UTC)
	arr := dep.Add(7 * time.Hour)
	return flights.FullOffer{
		Offer: flights.Offer{
			StartDate:  dep,
			ReturnDate: time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC),
			Price:      771,
		},
		Flight: []flights.Flight{{
			DepAirportCode: "JFK", ArrAirportCode: "LHR",
			DepCity: "New York", ArrCity: "London",
			DepTime: dep, ArrTime: arr,
			Duration: 7 * time.Hour, FlightNumber: "AA 104",
			AirlineName: "American", Airplane: "Boeing 777",
		}},
		SrcAirportCode: "JFK", DstAirportCode: "LHR",
		SrcCity: "New York", DstCity: "London",
		FlightDuration: 7 * time.Hour,
	}
}

func testCommon() commonOpts {
	return commonOpts{
		from: "JFK", to: "LHR", adults: 1, class: "economy", stops: "any",
		currency: "USD", lang: "en", tripType: "round-trip",
	}
}

func testArgs() flights.Args {
	return flights.Args{
		Date:        time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC),
		ReturnDate:  time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC),
		SrcAirports: []string{"JFK"},
		DstAirports: []string{"LHR"},
		Options: flights.Options{
			Travelers: flights.Travelers{Adults: 1},
			Currency:  currency.USD,
			Stops:     flights.AnyStops,
			Class:     flights.Economy,
			TripType:  flights.RoundTrip,
			Lang:      language.English,
		},
	}
}

func TestWriteOffersText(t *testing.T) {
	var buf bytes.Buffer
	pr := &flights.PriceRange{Low: 475, High: 640}
	writeOffersText(&buf, testCommon(), testArgs(), []flights.FullOffer{fixtureOffer()}, pr, "https://example.com/x")
	out := buf.String()
	for _, want := range []string{
		"JFK -> LHR", "depart 2026-07-06", "return 2026-07-21", "1 offers",
		"typical price range: 475 - 640 USD", "771.00 USD", "American AA 104",
		"JFK -> LHR", "book: https://example.com/x",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q:\n%s", want, out)
		}
	}
}

func TestWriteOffersJSON(t *testing.T) {
	var buf bytes.Buffer
	pr := &flights.PriceRange{Low: 475, High: 640}
	if err := writeOffersJSON(&buf, testCommon(), testArgs(), []flights.FullOffer{fixtureOffer()}, pr, "https://example.com/x"); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Type       string `json:"type"`
		Count      int    `json:"count"`
		URL        string `json:"url"`
		PriceRange *struct {
			Low  float64 `json:"low"`
			High float64 `json:"high"`
		} `json:"price_range"`
		Query struct {
			From   string `json:"from"`
			Depart string `json:"depart"`
			Return string `json:"return"`
		} `json:"query"`
		Offers []struct {
			Price           float64 `json:"price"`
			Currency        string  `json:"currency"`
			DurationMinutes int     `json:"duration_minutes"`
			Stops           int     `json:"stops"`
			Flights         []struct {
				Airline      string `json:"airline"`
				FlightNumber string `json:"flight_number"`
				From         string `json:"from"`
				To           string `json:"to"`
				Departure    string `json:"departure"`
			} `json:"flights"`
		} `json:"offers"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if out.Type != "offers" || out.Count != 1 || out.URL != "https://example.com/x" {
		t.Errorf("envelope = %+v", out)
	}
	if out.PriceRange == nil || out.PriceRange.Low != 475 {
		t.Errorf("price_range = %+v", out.PriceRange)
	}
	if out.Query.Depart != "2026-07-06" || out.Query.Return != "2026-07-21" {
		t.Errorf("query = %+v", out.Query)
	}
	o := out.Offers[0]
	if o.Price != 771 || o.Currency != "USD" || o.DurationMinutes != 420 || o.Stops != 0 {
		t.Errorf("offer = %+v", o)
	}
	f := o.Flights[0]
	if f.Airline != "American" || f.FlightNumber != "AA 104" || f.From != "JFK" || f.To != "LHR" {
		t.Errorf("flight = %+v", f)
	}
	if _, err := time.Parse(time.RFC3339, f.Departure); err != nil {
		t.Errorf("departure not RFC3339: %q", f.Departure)
	}
}

func TestWritePriceGraphTextAndJSON(t *testing.T) {
	offers := []flights.Offer{
		{StartDate: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), ReturnDate: time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC), Price: 714},
		{StartDate: time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC), ReturnDate: time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC), Price: 685},
	}
	args := flights.PriceGraphArgs{
		RangeStartDate: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		RangeEndDate:   time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC),
		TripLength:     7,
		Options:        testArgs().Options,
	}

	var buf bytes.Buffer
	writePriceGraphText(&buf, testCommon(), args, offers)
	out := buf.String()
	for _, want := range []string{"2026-07-01..2026-07-31", "7-day trip", "2 offers", "714.00 USD", "min 685.00  max 714.00"} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q:\n%s", want, out)
		}
	}

	buf.Reset()
	if err := writePriceGraphJSON(&buf, testCommon(), args, offers); err != nil {
		t.Fatal(err)
	}
	var j struct {
		Type   string `json:"type"`
		Count  int    `json:"count"`
		Offers []struct {
			Depart   string  `json:"depart"`
			Return   string  `json:"return"`
			Price    float64 `json:"price"`
			Currency string  `json:"currency"`
		} `json:"offers"`
	}
	if err := json.Unmarshal(buf.Bytes(), &j); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if j.Type != "pricegraph" || j.Count != 2 {
		t.Errorf("envelope = %+v", j)
	}
	if j.Offers[1].Depart != "2026-07-02" || j.Offers[1].Price != 685 || j.Offers[1].Currency != "USD" {
		t.Errorf("offer = %+v", j.Offers[1])
	}
}

func fixtureHotel() flights.Hotel {
	return flights.Hotel{
		Name: "Pod Times Square", Price: 134.5, BasePrice: 160, Currency: "USD",
		Stars: 3, Rating: 4.1, ReviewCount: 2554,
		Latitude: 40.76, Longitude: -73.98, ID: "10854294032695678956",
		CheckInDate:  time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC),
		CheckOutDate: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC),
	}
}

func testHotelCommon() hotelCommonOpts {
	return hotelCommonOpts{location: "New York", currency: "USD", lang: "en"}
}

func TestWriteHotelsTextAndJSON(t *testing.T) {
	ci := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	co := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	hotels := []flights.Hotel{fixtureHotel(), {Name: "Unpriced Inn", Currency: "USD"}}

	var buf bytes.Buffer
	writeHotelsText(&buf, testHotelCommon(), ci, co, hotels)
	out := buf.String()
	for _, want := range []string{"New York", "2026-09-30 -> 2026-10-03", "2 hotels", "Pod Times Square", "3*", "rating=4.1(2554)", "--", "Unpriced Inn"} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q:\n%s", want, out)
		}
	}

	buf.Reset()
	if err := writeHotelsJSON(&buf, testHotelCommon(), ci, co, hotels); err != nil {
		t.Fatal(err)
	}
	var j struct {
		Type  string `json:"type"`
		Count int    `json:"count"`
		Query struct {
			Location string `json:"location"`
			Nights   int    `json:"nights"`
		} `json:"query"`
		Hotels []struct {
			Name      string  `json:"name"`
			Price     float64 `json:"price"`
			BasePrice float64 `json:"base_price"`
			Stars     int     `json:"stars"`
		} `json:"hotels"`
	}
	if err := json.Unmarshal(buf.Bytes(), &j); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if j.Type != "hotels" || j.Count != 2 || j.Query.Nights != 3 {
		t.Errorf("envelope = %+v", j)
	}
	if h := j.Hotels[0]; h.Name != "Pod Times Square" || h.Price != 134.5 || h.BasePrice != 160 || h.Stars != 3 {
		t.Errorf("hotel = %+v", h)
	}
}

func TestWriteHotelPriceGraphTextAndJSON(t *testing.T) {
	offers := []flights.HotelDateOffer{
		{
			CheckInDate:  time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC),
			CheckOutDate: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC),
			Hotel:        fixtureHotel(),
		},
	}

	var buf bytes.Buffer
	writeHotelPriceGraphText(&buf, testHotelCommon(), 2, offers)
	out := buf.String()
	for _, want := range []string{"2-night stays", "1 dates", "2026-09-30", "Pod Times Square", "CHEAPEST:"} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q:\n%s", want, out)
		}
	}

	buf.Reset()
	if err := writeHotelPriceGraphJSON(&buf, testHotelCommon(), 2, offers); err != nil {
		t.Fatal(err)
	}
	var j struct {
		Type   string `json:"type"`
		Count  int    `json:"count"`
		Offers []struct {
			CheckIn   string  `json:"checkin"`
			CheckOut  string  `json:"checkout"`
			StayTotal float64 `json:"stay_total"`
			Hotel     struct {
				Name  string  `json:"name"`
				Price float64 `json:"price"`
			} `json:"hotel"`
		} `json:"offers"`
	}
	if err := json.Unmarshal(buf.Bytes(), &j); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if j.Type != "hotel-pricegraph" || j.Count != 1 {
		t.Errorf("envelope = %+v", j)
	}
	if o := j.Offers[0]; o.CheckIn != "2026-09-30" || o.Hotel.Name != "Pod Times Square" || o.Hotel.Price != 134.5 || o.StayTotal != 269 {
		t.Errorf("offer = %+v", o)
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
