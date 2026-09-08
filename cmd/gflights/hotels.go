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

	"github.com/b3r1itzx/google-flights-api/flights"
	"golang.org/x/text/currency"
	"golang.org/x/text/language"
)

type hotelCommonOpts struct {
	location string
	name     string
	minStars int
	maxStars int
	currency string
	lang     string
	jsonOut  bool
}

func registerHotelCommon(fs *flag.FlagSet, c *hotelCommonOpts) {
	fs.StringVar(&c.location, "location", "", "city, place, or ZIP code to search (required)")
	fs.StringVar(&c.name, "name", "", "only include hotels whose name contains this (case-insensitive); combine with --location \"<hotel name> <city>\" to track one hotel")
	fs.IntVar(&c.minStars, "min-stars", 0, "minimum hotel class 1-5 (0 = no minimum)")
	fs.IntVar(&c.maxStars, "max-stars", 0, "maximum hotel class 1-5 (0 = no maximum)")
	fs.StringVar(&c.currency, "currency", "USD", "ISO 4217 currency code")
	fs.StringVar(&c.lang, "lang", "en", "BCP 47 language tag")
	fs.BoolVar(&c.jsonOut, "json", false, "emit JSON instead of human-readable text")
}

func (c *hotelCommonOpts) resolve() (flights.HotelOptions, error) {
	var o flights.HotelOptions
	if c.location == "" {
		return o, errors.New("--location is required")
	}
	cur, err := currency.ParseISO(strings.ToUpper(c.currency))
	if err != nil {
		return o, fmt.Errorf("invalid --currency %q: %v", c.currency, err)
	}
	tag, err := language.Parse(c.lang)
	if err != nil {
		return o, fmt.Errorf("invalid --lang %q: %v", c.lang, err)
	}
	if c.minStars < 0 || c.minStars > 5 || c.maxStars < 0 || c.maxStars > 5 {
		return o, errors.New("--min-stars/--max-stars must be between 0 and 5")
	}
	o = flights.HotelOptions{
		MinStars:     c.minStars,
		MaxStars:     c.maxStars,
		NameContains: c.name,
		Currency:     cur,
		Lang:         tag,
	}
	return o, nil
}

// --- hotels subcommand ---

func runHotels(ctx context.Context, argv []string, out io.Writer) error {
	fs := flag.NewFlagSet("hotels", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var c hotelCommonOpts
	registerHotelCommon(fs, &c)
	var (
		checkin  = fs.String("checkin", "", "check-in date YYYY-MM-DD (required)")
		checkout = fs.String("checkout", "", "check-out date YYYY-MM-DD (required)")
		limit    = fs.Int("limit", 20, "maximum number of hotels to show (0 = no limit)")
		sortBy   = fs.String("sort", "price", "sort by: price|stars|rating")
	)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `hotels — hotel offers for a location and stay dates.

USAGE
  gflights hotels --location <city|ZIP> --checkin <YYYY-MM-DD> --checkout <YYYY-MM-DD> [flags]

EXAMPLE
  gflights hotels --location "New York" --checkin 2026-06-01 --checkout 2026-06-11
  gflights hotels --location 10065 --checkin 2026-06-01 --checkout 2026-06-11 --min-stars 4 --json

FLAGS
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(argv); err != nil {
		return err
	}
	if *checkin == "" || *checkout == "" {
		fs.Usage()
		return errors.New("--checkin and --checkout are required")
	}
	ci, err := parseDate(*checkin, "checkin")
	if err != nil {
		return err
	}
	co, err := parseDate(*checkout, "checkout")
	if err != nil {
		return err
	}
	opts, err := c.resolve()
	if err != nil {
		return err
	}

	hotelArgs := flights.HotelArgs{
		Location:     c.location,
		CheckInDate:  ci,
		CheckOutDate: co,
		HotelOptions: opts,
	}
	if err := hotelArgs.Validate(); err != nil {
		return err
	}

	sess, err := flights.NewBrowserSession()
	if err != nil {
		return fmt.Errorf("session: %v", err)
	}
	defer sess.Close()

	hotels, err := sess.GetHotelOffers(ctx, hotelArgs)
	if err != nil {
		return err
	}

	switch strings.ToLower(*sortBy) {
	case "price", "":
		// already price-sorted by the library
	case "stars":
		sort.SliceStable(hotels, func(i, j int) bool { return hotels[i].Stars > hotels[j].Stars })
	case "rating":
		sort.SliceStable(hotels, func(i, j int) bool { return hotels[i].Rating > hotels[j].Rating })
	default:
		return fmt.Errorf("invalid --sort %q (want price|stars|rating)", *sortBy)
	}

	if *limit > 0 && len(hotels) > *limit {
		hotels = hotels[:*limit]
	}

	if c.jsonOut {
		return writeHotelsJSON(out, c, ci, co, hotels)
	}
	writeHotelsText(out, c, ci, co, hotels)
	return nil
}

func starStr(n int) string {
	if n <= 0 {
		return "  -"
	}
	return fmt.Sprintf("%d*", n)
}

func writeHotelsText(w io.Writer, c hotelCommonOpts, ci, co time.Time, hotels []flights.Hotel) {
	fmt.Fprintf(w, "%s  |  %s -> %s  |  %d hotels\n",
		c.location, ci.Format("2006-01-02"), co.Format("2006-01-02"), len(hotels))
	for i, h := range hotels {
		price := "   --"
		if h.Price > 0 {
			price = fmt.Sprintf("%.0f", h.Price)
		}
		fmt.Fprintf(w, "  %-6s %s  %-4s rating=%.1f(%d)  %s\n",
			price, h.Currency, starStr(h.Stars), h.Rating, h.ReviewCount, h.Name)
		_ = i
	}
}

type hotelQueryJSON struct {
	Location string `json:"location"`
	Name     string `json:"name,omitempty"`
	CheckIn  string `json:"checkin"`
	CheckOut string `json:"checkout"`
	Nights   int    `json:"nights"`
	MinStars int    `json:"min_stars,omitempty"`
	MaxStars int    `json:"max_stars,omitempty"`
	Currency string `json:"currency"`
	Lang     string `json:"lang"`
}

type hotelJSON struct {
	Name        string  `json:"name"`
	Price       float64 `json:"price"`
	BasePrice   float64 `json:"base_price,omitempty"`
	Currency    string  `json:"currency"`
	Stars       int     `json:"stars"`
	Rating      float64 `json:"rating"`
	ReviewCount int     `json:"review_count"`
	Description string  `json:"description,omitempty"`
	Latitude    float64 `json:"latitude,omitempty"`
	Longitude   float64 `json:"longitude,omitempty"`
	ID          string  `json:"id,omitempty"`
}

func toHotelJSON(h flights.Hotel) hotelJSON {
	return hotelJSON{
		Name: h.Name, Price: h.Price, BasePrice: h.BasePrice, Currency: h.Currency,
		Stars: h.Stars, Rating: h.Rating, ReviewCount: h.ReviewCount,
		Description: h.Description, Latitude: h.Latitude, Longitude: h.Longitude, ID: h.ID,
	}
}

func hotelQuery(c hotelCommonOpts, ci, co time.Time) hotelQueryJSON {
	return hotelQueryJSON{
		Location: c.location,
		Name:     c.name,
		CheckIn:  ci.Format("2006-01-02"),
		CheckOut: co.Format("2006-01-02"),
		Nights:   int(co.Sub(ci).Hours() / 24),
		MinStars: c.minStars,
		MaxStars: c.maxStars,
		Currency: strings.ToUpper(c.currency),
		Lang:     c.lang,
	}
}

func writeHotelsJSON(w io.Writer, c hotelCommonOpts, ci, co time.Time, hotels []flights.Hotel) error {
	out := struct {
		Type   string         `json:"type"`
		Query  hotelQueryJSON `json:"query"`
		Count  int            `json:"count"`
		Hotels []hotelJSON    `json:"hotels"`
	}{Type: "hotels", Query: hotelQuery(c, ci, co), Count: len(hotels)}
	for _, h := range hotels {
		out.Hotels = append(out.Hotels, toHotelJSON(h))
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// --- hotel-pricegraph subcommand ---

func runHotelPriceGraph(ctx context.Context, argv []string, out io.Writer) error {
	fs := flag.NewFlagSet("hotel-pricegraph", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var c hotelCommonOpts
	registerHotelCommon(fs, &c)
	var (
		start  = fs.String("start", "", "earliest check-in date YYYY-MM-DD (required)")
		end    = fs.String("end", "", "latest check-in date YYYY-MM-DD (required)")
		nights = fs.Int("nights", 7, "length of stay in nights")
		step   = fs.Int("step", 1, "days between sampled check-in dates")
	)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `hotel-pricegraph — cheapest hotel per check-in date over a range.

USAGE
  gflights hotel-pricegraph --location <city|ZIP> --start <YYYY-MM-DD> --end <YYYY-MM-DD> --nights <n> [flags]

EXAMPLE
  gflights hotel-pricegraph --location "New York" --start 2026-06-01 --end 2026-06-20 --nights 10 --min-stars 4
  gflights hotel-pricegraph --location 10065 --start 2026-06-01 --end 2026-06-30 --nights 10 --step 2 --json

NOTE
  One request per sampled date — wide ranges with --step 1 are slow. Prices are
  the cheapest star-matching hotel's nightly rate for each window.

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
	opts, err := c.resolve()
	if err != nil {
		return err
	}

	hpgArgs := flights.HotelPriceGraphArgs{
		Location:       c.location,
		RangeStartDate: startD,
		RangeEndDate:   endD,
		Nights:         *nights,
		StepDays:       *step,
		HotelOptions:   opts,
	}
	if err := hpgArgs.Validate(); err != nil {
		return err
	}

	sess, err := flights.NewBrowserSession()
	if err != nil {
		return fmt.Errorf("session: %v", err)
	}
	defer sess.Close()

	offers, err := sess.GetHotelPriceGraph(ctx, hpgArgs)
	if err != nil {
		return err
	}

	if c.jsonOut {
		return writeHotelPriceGraphJSON(out, c, *nights, offers)
	}
	writeHotelPriceGraphText(out, c, *nights, offers)
	return nil
}

func writeHotelPriceGraphText(w io.Writer, c hotelCommonOpts, nights int, offers []flights.HotelDateOffer) {
	fmt.Fprintf(w, "%s  |  %d-night stays  |  %d dates\n", c.location, nights, len(offers))
	fmt.Fprintf(w, "%-12s  %-12s  %10s  %14s\n", "CHECK-IN", "CHECK-OUT", "PER NIGHT", "STAY TOTAL")
	fmt.Fprintf(w, "%s\n", strings.Repeat("-", 52))
	var best *flights.HotelDateOffer
	for i := range offers {
		o := offers[i]
		fmt.Fprintf(w, "%-12s  %-12s  %8.0f %s  %10.0f %s  %-4s %s\n",
			o.CheckInDate.Format("2006-01-02"), o.CheckOutDate.Format("2006-01-02"),
			o.Hotel.Price, o.Hotel.Currency, o.Hotel.Price*float64(nights), o.Hotel.Currency,
			starStr(o.Hotel.Stars), o.Hotel.Name)
		if best == nil || o.Hotel.Price < best.Hotel.Price {
			best = &offers[i]
		}
	}
	if best != nil {
		fmt.Fprintf(w, "\nCHEAPEST: %s -> %s  %.0f %s/night, %.0f %s total  %s %s\n",
			best.CheckInDate.Format("2006-01-02"), best.CheckOutDate.Format("2006-01-02"),
			best.Hotel.Price, best.Hotel.Currency,
			best.Hotel.Price*float64(nights), best.Hotel.Currency,
			starStr(best.Hotel.Stars), best.Hotel.Name)
	}
}

func writeHotelPriceGraphJSON(w io.Writer, c hotelCommonOpts, nights int, offers []flights.HotelDateOffer) error {
	type dateOfferJSON struct {
		CheckIn   string    `json:"checkin"`
		CheckOut  string    `json:"checkout"`
		StayTotal float64   `json:"stay_total"`
		Hotel     hotelJSON `json:"hotel"`
	}
	out := struct {
		Type   string          `json:"type"`
		Query  hotelQueryJSON  `json:"query"`
		Count  int             `json:"count"`
		Offers []dateOfferJSON `json:"offers"`
	}{Type: "hotel-pricegraph", Count: len(offers)}
	out.Query = hotelQueryJSON{
		Location: c.location, Name: c.name, Nights: nights,
		MinStars: c.minStars, MaxStars: c.maxStars,
		Currency: strings.ToUpper(c.currency), Lang: c.lang,
	}
	for _, o := range offers {
		out.Offers = append(out.Offers, dateOfferJSON{
			CheckIn:   o.CheckInDate.Format("2006-01-02"),
			CheckOut:  o.CheckOutDate.Format("2006-01-02"),
			StayTotal: o.Hotel.Price * float64(nights),
			Hotel:     toHotelJSON(o.Hotel),
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
