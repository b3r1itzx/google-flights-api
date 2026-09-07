// Command gflights is a CLI wrapper around the google-flights-api library.
// It exposes two subcommands — `pricegraph` and `offers` — and supports both
// human-readable text output and machine-readable JSON (`--json`) intended for
// agent consumption.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/b3r1itzx/google-flights-api/flights"
	"golang.org/x/text/currency"
	"golang.org/x/text/language"
)

const usageRoot = `gflights — query Google Flights from the command line.

USAGE
  gflights <subcommand> [flags]

SUBCOMMANDS
  pricegraph        Cheapest round-trip flight price per departure date over a range.
  offers            Detailed flight offers for a specific departure (+ return) date.
  deals             Cheapest destinations from an origin, ranked (fan-out explore).
  hotels            Hotel offers for a location and stay dates.
  hotel-pricegraph  Cheapest hotel per check-in date over a range (fixed nights).

GLOBAL FLAGS (any subcommand)
  --json       Emit a JSON document on stdout instead of a human table.
  --help       Show help for a subcommand.

Run "gflights <subcommand> --help" for subcommand flags.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usageRoot)
		os.Exit(2)
	}
	sub := os.Args[1]
	args := os.Args[2:]
	var err error
	switch sub {
	case "pricegraph":
		err = runPriceGraph(args, os.Stdout)
	case "offers":
		err = runOffers(args, os.Stdout)
	case "deals":
		err = runDeals(args, os.Stdout)
	case "hotels":
		err = runHotels(args, os.Stdout)
	case "hotel-pricegraph":
		err = runHotelPriceGraph(args, os.Stdout)
	case "-h", "--help", "help":
		fmt.Print(usageRoot)
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %q\n\n%s", sub, usageRoot)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// --- shared flag plumbing ---

type commonOpts struct {
	from        string
	to          string
	adults      int
	children    int
	infantsLap  int
	infantsSeat int
	class       string
	stops       string
	currency    string
	lang        string
	tripType    string
	jsonOut     bool
}

func registerCommon(fs *flag.FlagSet, c *commonOpts) {
	fs.StringVar(&c.from, "from", "", "source: city name or IATA code; comma-separated for multiple (required)")
	fs.StringVar(&c.to, "to", "", "destination: city name or IATA code; comma-separated for multiple (required)")
	fs.IntVar(&c.adults, "adults", 1, "number of adult travelers")
	fs.IntVar(&c.children, "children", 0, "number of children")
	fs.IntVar(&c.infantsLap, "infants-lap", 0, "number of infants on lap")
	fs.IntVar(&c.infantsSeat, "infants-seat", 0, "number of infants in seat")
	fs.StringVar(&c.class, "class", "economy", "class: economy|premium-economy|business|first")
	fs.StringVar(&c.stops, "stops", "any", "stops: any|nonstop|stop1|stop2")
	fs.StringVar(&c.currency, "currency", "USD", "ISO 4217 currency code")
	fs.StringVar(&c.lang, "lang", "en", "BCP 47 language tag for city-name resolution")
	fs.StringVar(&c.tripType, "trip-type", "round-trip", "trip type: round-trip|one-way")
	fs.BoolVar(&c.jsonOut, "json", false, "emit JSON instead of human-readable text")
}

// resolveOptions parses the traveler/class/stops/currency/lang/trip-type flags
// into flights.Options. It does not require a destination, so origin-only
// commands (deals) can share it.
func (c *commonOpts) resolveOptions() (flights.Options, error) {
	var opts flights.Options

	cls, err := parseClass(c.class)
	if err != nil {
		return opts, err
	}
	stops, err := parseStops(c.stops)
	if err != nil {
		return opts, err
	}
	tt, err := parseTripType(c.tripType)
	if err != nil {
		return opts, err
	}
	cur, err := currency.ParseISO(strings.ToUpper(c.currency))
	if err != nil {
		return opts, fmt.Errorf("invalid --currency %q: %v", c.currency, err)
	}
	tag, err := language.Parse(c.lang)
	if err != nil {
		return opts, fmt.Errorf("invalid --lang %q: %v", c.lang, err)
	}

	opts = flights.Options{
		Travelers: flights.Travelers{
			Adults:       c.adults,
			Children:     c.children,
			InfantInSeat: c.infantsSeat,
			InfantOnLap:  c.infantsLap,
		},
		Currency: cur,
		Stops:    stops,
		Class:    cls,
		TripType: tt,
		Lang:     tag,
	}
	return opts, nil
}

func (c *commonOpts) resolve() (flights.Options, srcDst, error) {
	var sd srcDst

	if c.from == "" || c.to == "" {
		return flights.Options{}, sd, errors.New("--from and --to are required")
	}
	opts, err := c.resolveOptions()
	if err != nil {
		return opts, sd, err
	}
	sd.srcCities, sd.srcAirports = splitLocations(c.from)
	sd.dstCities, sd.dstAirports = splitLocations(c.to)
	return opts, sd, nil
}

type srcDst struct {
	srcCities, srcAirports []string
	dstCities, dstAirports []string
}

// splitLocations partitions a comma-separated list into IATA codes (3 upper
// letters) and free-form city names. Strips surrounding whitespace.
func splitLocations(s string) (cities, airports []string) {
	for _, tok := range strings.Split(s, ",") {
		t := strings.TrimSpace(tok)
		if t == "" {
			continue
		}
		if isAirportCode(t) {
			airports = append(airports, t)
		} else {
			cities = append(cities, t)
		}
	}
	return cities, airports
}

func isAirportCode(s string) bool {
	if len(s) != 3 {
		return false
	}
	for _, r := range s {
		if !unicode.IsUpper(r) {
			return false
		}
	}
	return true
}

func parseClass(s string) (flights.Class, error) {
	switch strings.ToLower(strings.ReplaceAll(s, "_", "-")) {
	case "economy":
		return flights.Economy, nil
	case "premium-economy", "premium":
		return flights.PremiumEconomy, nil
	case "business":
		return flights.Business, nil
	case "first":
		return flights.First, nil
	}
	return 0, fmt.Errorf("invalid --class %q (want economy|premium-economy|business|first)", s)
}

func parseStops(s string) (flights.Stops, error) {
	switch strings.ToLower(s) {
	case "any", "":
		return flights.AnyStops, nil
	case "nonstop", "0":
		return flights.Nonstop, nil
	case "stop1", "1":
		return flights.Stop1, nil
	case "stop2", "2":
		return flights.Stop2, nil
	}
	return 0, fmt.Errorf("invalid --stops %q (want any|nonstop|stop1|stop2)", s)
}

func parseTripType(s string) (flights.TripType, error) {
	switch strings.ToLower(strings.ReplaceAll(s, "_", "-")) {
	case "round-trip", "round", "rt":
		return flights.RoundTrip, nil
	case "one-way", "oneway", "ow":
		return flights.OneWay, nil
	}
	return 0, fmt.Errorf("invalid --trip-type %q (want round-trip|one-way)", s)
}

func parseDate(s, flagName string) (time.Time, error) {
	t, err := time.ParseInLocation("2006-01-02", s, time.UTC)
	if err != nil {
		return t, fmt.Errorf("--%s must be YYYY-MM-DD (got %q)", flagName, s)
	}
	return t, nil
}

// --- pricegraph subcommand ---

func runPriceGraph(argv []string, out io.Writer) error {
	fs := flag.NewFlagSet("pricegraph", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var c commonOpts
	registerCommon(fs, &c)
	var (
		start    = fs.String("start", "", "range start date YYYY-MM-DD (required)")
		end      = fs.String("end", "", "range end date YYYY-MM-DD (required); must be within 161 days of start")
		duration = fs.Int("duration", 7, "trip length in days")
		sortBy   = fs.String("sort", "date", "sort offers by: date|price")
	)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `pricegraph — cheapest round-trip price per departure date over a range.

USAGE
  gflights pricegraph --from <city|IATA> --to <city|IATA> --start <YYYY-MM-DD> --end <YYYY-MM-DD> --duration <days> [flags]

EXAMPLE
  gflights pricegraph --from "New York" --to Rome --start 2026-07-01 --end 2026-07-31 --duration 15
  gflights pricegraph --from JFK --to FCO --start 2026-07-01 --end 2026-07-31 --duration 15 --json

FLAGS
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(argv); err != nil {
		return err
	}
	if *start == "" || *end == "" {
		fs.Usage()
		return errors.New("--start and --end are required")
	}
	startD, err := parseDate(*start, "start")
	if err != nil {
		return err
	}
	endD, err := parseDate(*end, "end")
	if err != nil {
		return err
	}
	opts, sd, err := c.resolve()
	if err != nil {
		return err
	}

	sess, err := flights.NewBrowserSession()
	if err != nil {
		return fmt.Errorf("session: %v", err)
	}
	defer sess.Close()

	args := flights.PriceGraphArgs{
		RangeStartDate: startD,
		RangeEndDate:   endD,
		TripLength:     *duration,
		SrcCities:      sd.srcCities,
		SrcAirports:    sd.srcAirports,
		DstCities:      sd.dstCities,
		DstAirports:    sd.dstAirports,
		Options:        opts,
	}
	offers, err := sess.GetPriceGraph(context.Background(), args)
	if err != nil {
		return err
	}

	switch strings.ToLower(*sortBy) {
	case "price":
		sort.SliceStable(offers, func(i, j int) bool { return offers[i].Price < offers[j].Price })
	case "date", "":
		sort.SliceStable(offers, func(i, j int) bool { return offers[i].StartDate.Before(offers[j].StartDate) })
	default:
		return fmt.Errorf("invalid --sort %q (want date|price)", *sortBy)
	}

	if c.jsonOut {
		return writePriceGraphJSON(out, c, args, offers)
	}
	writePriceGraphText(out, c, args, offers)
	return nil
}

func writePriceGraphText(w io.Writer, c commonOpts, args flights.PriceGraphArgs, offers []flights.Offer) {
	fmt.Fprintf(w, "%s -> %s  |  %s..%s  |  %d-day trip  |  %d offers\n",
		c.from, c.to,
		args.RangeStartDate.Format("2006-01-02"),
		args.RangeEndDate.Format("2006-01-02"),
		args.TripLength, len(offers),
	)
	fmt.Fprintf(w, "%-12s  %-12s  %12s\n", "DEPART", "RETURN", "PRICE")
	fmt.Fprintf(w, "%s\n", strings.Repeat("-", 40))
	var min, max float64
	for i, o := range offers {
		fmt.Fprintf(w, "%-12s  %-12s  %10.2f %s\n",
			o.StartDate.Format("2006-01-02"),
			o.ReturnDate.Format("2006-01-02"),
			o.Price, args.Currency,
		)
		if i == 0 || o.Price < min {
			min = o.Price
		}
		if o.Price > max {
			max = o.Price
		}
	}
	if len(offers) > 0 {
		fmt.Fprintf(w, "\nmin %.2f  max %.2f  %s\n", min, max, args.Currency)
	}
}

type priceGraphJSON struct {
	Type   string         `json:"type"`
	Query  queryJSON      `json:"query"`
	Count  int            `json:"count"`
	Offers []pgOfferJSON  `json:"offers"`
}

type pgOfferJSON struct {
	Depart   string  `json:"depart"`
	Return   string  `json:"return"`
	Price    float64 `json:"price"`
	Currency string  `json:"currency"`
}

func writePriceGraphJSON(w io.Writer, c commonOpts, args flights.PriceGraphArgs, offers []flights.Offer) error {
	out := priceGraphJSON{
		Type:  "pricegraph",
		Query: queryFromCommon(c, args.RangeStartDate, args.RangeEndDate, args.TripLength, ""),
		Count: len(offers),
	}
	for _, o := range offers {
		out.Offers = append(out.Offers, pgOfferJSON{
			Depart:   o.StartDate.Format("2006-01-02"),
			Return:   o.ReturnDate.Format("2006-01-02"),
			Price:    o.Price,
			Currency: args.Currency.String(),
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// --- offers subcommand ---

func runOffers(argv []string, out io.Writer) error {
	fs := flag.NewFlagSet("offers", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var c commonOpts
	registerCommon(fs, &c)
	var (
		depart  = fs.String("depart", "", "departure date YYYY-MM-DD (required)")
		ret     = fs.String("return", "", "return date YYYY-MM-DD (required for round-trip)")
		limit   = fs.Int("limit", 20, "maximum number of offers to show (0 = no limit)")
		sortBy  = fs.String("sort", "price", "sort offers by: price|duration|departure")
		withURL = fs.Bool("url", true, "include the Google Flights URL in the output")
	)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `offers — detailed flight offers for a specific departure (+ return) date.

USAGE
  gflights offers --from <city|IATA> --to <city|IATA> --depart <YYYY-MM-DD> [--return <YYYY-MM-DD>] [flags]

EXAMPLE
  gflights offers --from "New York" --to Rome --depart 2026-07-06 --return 2026-07-21
  gflights offers --from JFK --to FCO --depart 2026-07-06 --return 2026-07-21 --json --limit 5
  gflights offers --from JFK --to FCO --depart 2026-07-06 --trip-type one-way

FLAGS
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(argv); err != nil {
		return err
	}
	if *depart == "" {
		fs.Usage()
		return errors.New("--depart is required")
	}
	departD, err := parseDate(*depart, "depart")
	if err != nil {
		return err
	}
	var returnD time.Time
	opts, sd, err := c.resolve()
	if err != nil {
		return err
	}
	if opts.TripType == flights.RoundTrip {
		if *ret == "" {
			fs.Usage()
			return errors.New("--return is required for round-trip; pass --trip-type one-way for a one-way query")
		}
		returnD, err = parseDate(*ret, "return")
		if err != nil {
			return err
		}
	} else {
		// One-way: lib still wants a non-zero return date for validation, so
		// pass the same day as the departure (the upstream API ignores it for
		// one-way trips because TripType=OneWay drives the request body).
		returnD = departD
	}

	sess, err := flights.NewBrowserSession()
	if err != nil {
		return fmt.Errorf("session: %v", err)
	}
	defer sess.Close()

	args := flights.Args{
		Date:        departD,
		ReturnDate:  returnD,
		SrcCities:   sd.srcCities,
		SrcAirports: sd.srcAirports,
		DstCities:   sd.dstCities,
		DstAirports: sd.dstAirports,
		Options:     opts,
	}
	offers, priceRange, err := sess.GetOffers(context.Background(), args)
	if err != nil {
		return err
	}

	switch strings.ToLower(*sortBy) {
	case "price", "":
		sort.SliceStable(offers, func(i, j int) bool { return offers[i].Price < offers[j].Price })
	case "duration":
		sort.SliceStable(offers, func(i, j int) bool { return offers[i].FlightDuration < offers[j].FlightDuration })
	case "departure":
		sort.SliceStable(offers, func(i, j int) bool {
			if len(offers[i].Flight) == 0 || len(offers[j].Flight) == 0 {
				return false
			}
			return offers[i].Flight[0].DepTime.Before(offers[j].Flight[0].DepTime)
		})
	default:
		return fmt.Errorf("invalid --sort %q (want price|duration|departure)", *sortBy)
	}

	if *limit > 0 && len(offers) > *limit {
		offers = offers[:*limit]
	}

	var url string
	if *withURL {
		// SerializeURL can fail independently (network); don't fail the whole
		// command over it — just skip the URL in output.
		if u, uerr := sess.SerializeURL(context.Background(), args); uerr == nil {
			url = u
		}
	}

	if c.jsonOut {
		return writeOffersJSON(out, c, args, offers, priceRange, url)
	}
	writeOffersText(out, c, args, offers, priceRange, url)
	return nil
}

func writeOffersText(w io.Writer, c commonOpts, args flights.Args, offers []flights.FullOffer, pr *flights.PriceRange, url string) {
	fmt.Fprintf(w, "%s -> %s  |  depart %s",
		c.from, c.to, args.Date.Format("2006-01-02"))
	if args.TripType == flights.RoundTrip {
		fmt.Fprintf(w, ", return %s", args.ReturnDate.Format("2006-01-02"))
	} else {
		fmt.Fprint(w, " (one-way)")
	}
	fmt.Fprintf(w, "  |  %d offers\n", len(offers))
	if pr != nil {
		fmt.Fprintf(w, "typical price range: %.0f - %.0f %s\n", pr.Low, pr.High, args.Currency)
	}
	for i, o := range offers {
		stops := 0
		if len(o.Flight) > 0 {
			stops = len(o.Flight) - 1
		}
		fmt.Fprintf(w, "\n#%d  %.2f %s  duration=%s  stops=%d\n",
			i+1, o.Price, args.Currency, o.FlightDuration.Round(time.Minute), stops)
		for _, f := range o.Flight {
			fmt.Fprintf(w, "    %s %s  %s -> %s  %s -> %s  (%s)\n",
				f.AirlineName, f.FlightNumber,
				f.DepAirportCode, f.ArrAirportCode,
				f.DepTime.Format("Jan 02 15:04"),
				f.ArrTime.Format("Jan 02 15:04"),
				f.Duration.Round(time.Minute),
			)
		}
	}
	if url != "" {
		fmt.Fprintf(w, "\nbook: %s\n", url)
	}
}

type offersJSON struct {
	Type       string           `json:"type"`
	Query      queryJSON        `json:"query"`
	PriceRange *priceRangeJSON  `json:"price_range,omitempty"`
	URL        string           `json:"url,omitempty"`
	Count      int              `json:"count"`
	Offers     []fullOfferJSON  `json:"offers"`
}

type priceRangeJSON struct {
	Low  float64 `json:"low"`
	High float64 `json:"high"`
}

type fullOfferJSON struct {
	Price           float64      `json:"price"`
	Currency        string       `json:"currency"`
	DurationMinutes int          `json:"duration_minutes"`
	Stops           int          `json:"stops"`
	SrcAirport      string       `json:"src_airport"`
	DstAirport      string       `json:"dst_airport"`
	SrcCity         string       `json:"src_city,omitempty"`
	DstCity         string       `json:"dst_city,omitempty"`
	Flights         []flightJSON `json:"flights"`
}

type flightJSON struct {
	Airline         string `json:"airline"`
	FlightNumber    string `json:"flight_number"`
	From            string `json:"from"`
	FromName        string `json:"from_name,omitempty"`
	FromCity        string `json:"from_city,omitempty"`
	To              string `json:"to"`
	ToName          string `json:"to_name,omitempty"`
	ToCity          string `json:"to_city,omitempty"`
	Departure       string `json:"departure"`
	Arrival         string `json:"arrival"`
	DurationMinutes int    `json:"duration_minutes"`
	Airplane        string `json:"airplane,omitempty"`
	Legroom         string `json:"legroom,omitempty"`
}

func writeOffersJSON(w io.Writer, c commonOpts, args flights.Args, offers []flights.FullOffer, pr *flights.PriceRange, url string) error {
	out := offersJSON{
		Type:  "offers",
		Query: queryFromCommon(c, args.Date, args.ReturnDate, 0, url),
		Count: len(offers),
		URL:   url,
	}
	if pr != nil {
		out.PriceRange = &priceRangeJSON{Low: pr.Low, High: pr.High}
	}
	for _, o := range offers {
		fo := fullOfferJSON{
			Price:           o.Price,
			Currency:        args.Currency.String(),
			DurationMinutes: int(o.FlightDuration.Minutes()),
			SrcAirport:      o.SrcAirportCode,
			DstAirport:      o.DstAirportCode,
			SrcCity:         o.SrcCity,
			DstCity:         o.DstCity,
		}
		if len(o.Flight) > 0 {
			fo.Stops = len(o.Flight) - 1
		}
		for _, f := range o.Flight {
			fo.Flights = append(fo.Flights, flightJSON{
				Airline:         f.AirlineName,
				FlightNumber:    f.FlightNumber,
				From:            f.DepAirportCode,
				FromName:        f.DepAirportName,
				FromCity:        f.DepCity,
				To:              f.ArrAirportCode,
				ToName:          f.ArrAirportName,
				ToCity:          f.ArrCity,
				Departure:       f.DepTime.Format(time.RFC3339),
				Arrival:         f.ArrTime.Format(time.RFC3339),
				DurationMinutes: int(f.Duration.Minutes()),
				Airplane:        f.Airplane,
				Legroom:         f.Legroom,
			})
		}
		out.Offers = append(out.Offers, fo)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// --- shared JSON shape ---

type queryJSON struct {
	From       string         `json:"from"`
	To         string         `json:"to"`
	Depart     string         `json:"depart,omitempty"`
	Return     string         `json:"return,omitempty"`
	RangeStart string         `json:"range_start,omitempty"`
	RangeEnd   string         `json:"range_end,omitempty"`
	Duration   int            `json:"duration_days,omitempty"`
	Travelers  travelersJSON  `json:"travelers"`
	Class      string         `json:"class"`
	Stops      string         `json:"stops"`
	TripType   string         `json:"trip_type"`
	Currency   string         `json:"currency"`
	Lang       string         `json:"lang"`
}

type travelersJSON struct {
	Adults       int `json:"adults"`
	Children     int `json:"children"`
	InfantsLap   int `json:"infants_lap"`
	InfantsSeat  int `json:"infants_seat"`
}

// queryFromCommon builds the query echo block. For pricegraph callers, pass
// the range dates via d1/d2 and duration > 0; for offers callers, pass depart
// via d1 and return via d2 (or zero) with duration == 0.
func queryFromCommon(c commonOpts, d1, d2 time.Time, duration int, _ string) queryJSON {
	q := queryJSON{
		From: c.from,
		To:   c.to,
		Travelers: travelersJSON{
			Adults:      c.adults,
			Children:    c.children,
			InfantsLap:  c.infantsLap,
			InfantsSeat: c.infantsSeat,
		},
		Class:    c.class,
		Stops:    c.stops,
		TripType: c.tripType,
		Currency: strings.ToUpper(c.currency),
		Lang:     c.lang,
	}
	if duration > 0 {
		q.RangeStart = d1.Format("2006-01-02")
		q.RangeEnd = d2.Format("2006-01-02")
		q.Duration = duration
	} else {
		q.Depart = d1.Format("2006-01-02")
		if !d2.IsZero() && !d2.Equal(d1) {
			q.Return = d2.Format("2006-01-02")
		}
	}
	return q
}
