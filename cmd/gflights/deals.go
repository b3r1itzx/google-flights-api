package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/b3r1itzx/google-flights-api/flights"
)

// destinationPresets are built-in destination lists so callers don't have to
// type long airport lists. Keys are passed to --preset.
var destinationPresets = map[string][]string{
	// ~30 busiest US airports by passenger volume.
	"us-major": {
		"ATL", "LAX", "ORD", "DFW", "DEN", "JFK", "SFO", "LAS", "MCO", "SEA",
		"EWR", "MIA", "PHX", "IAH", "BOS", "MSP", "FLL", "DTW", "PHL", "LGA",
		"CLT", "BWI", "SLC", "SAN", "IAD", "DCA", "TPA", "PDX", "HNL", "AUS",
	},
}

// deal is the cheapest round-trip found for one destination.
type deal struct {
	Dest     string  `json:"dest"`
	Price    float64 `json:"price"`
	Depart   string  `json:"depart"`
	Return   string  `json:"return"`
	Currency string  `json:"currency"`
}

func runDeals(argv []string, out io.Writer) error {
	fs := flag.NewFlagSet("deals", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var c commonOpts
	registerCommon(fs, &c)
	var (
		start       = fs.String("start", "", "range start date YYYY-MM-DD (required)")
		end         = fs.String("end", "", "range end date YYYY-MM-DD (required); within 161 days of start")
		duration    = fs.Int("duration", 7, "trip length in days")
		preset      = fs.String("preset", "", "built-in destination set to include: "+presetNames())
		concurrency = fs.Int("concurrency", 4, "number of destinations to search in parallel")
		limit       = fs.Int("limit", 0, "show only the N cheapest destinations (0 = all)")
	)
	fs.Usage = func() {
		fmt.Fprint(out, `deals — cheapest destinations from an origin, ranked (fan-out explore).

Runs one price-graph search per destination and reports the cheapest
round-trip per destination, sorted cheapest first. Destinations come from
--to (comma-separated) and/or --preset. This is the origin-wide "explore"
the underlying API has no single call for.

USAGE
  gflights deals --from <city|IATA> [--to <d1,d2,...>] [--preset us-major] \
      --start <YYYY-MM-DD> --end <YYYY-MM-DD> [--duration <days>] [flags]

EXAMPLE
  gflights deals --from ORF --preset us-major --start 2026-11-01 --end 2026-11-30 --duration 4 --limit 10
  gflights deals --from ORF --to MCO,ATL,LAS,DEN --start 2026-11-01 --end 2026-11-30 --json

FLAGS
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(argv); err != nil {
		return err
	}
	if c.from == "" {
		fs.Usage()
		return errors.New("--from is required")
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

	dests, err := resolveDestinations(c.to, *preset, c.from)
	if err != nil {
		return err
	}
	opts, err := c.resolveOptions()
	if err != nil {
		return err
	}
	if *concurrency < 1 {
		*concurrency = 1
	}

	srcCities, srcAirports := splitLocations(c.from)

	sess, err := flights.NewBrowserSession()
	if err != nil {
		return fmt.Errorf("session: %v", err)
	}
	defer sess.Close()

	deals, failures := searchDeals(context.Background(), sess, dealParams{
		srcCities: srcCities, srcAirports: srcAirports,
		start: startD, end: endD, duration: *duration,
		opts: opts, dests: dests, concurrency: *concurrency,
	})

	sort.SliceStable(deals, func(i, j int) bool { return deals[i].Price < deals[j].Price })
	totalWithOffers := len(deals)
	if *limit > 0 && len(deals) > *limit {
		deals = deals[:*limit]
	}

	if c.jsonOut {
		return writeDealsJSON(out, c, startD, endD, *duration, len(dests), totalWithOffers, deals, failures)
	}
	writeDealsText(out, c, startD, endD, *duration, len(dests), totalWithOffers, deals, failures)
	return nil
}

func presetNames() string {
	names := make([]string, 0, len(destinationPresets))
	for k := range destinationPresets {
		names = append(names, k)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}

// resolveDestinations builds the destination list from a comma-separated --to
// and an optional preset, de-duplicated and with the origin removed. Origin
// tokens are compared case-insensitively against each destination token.
func resolveDestinations(to, preset, from string) ([]string, error) {
	var list []string
	for _, tok := range strings.Split(to, ",") {
		if t := strings.TrimSpace(tok); t != "" {
			list = append(list, t)
		}
	}
	if preset != "" {
		p, ok := destinationPresets[preset]
		if !ok {
			return nil, fmt.Errorf("unknown --preset %q (have: %s)", preset, presetNames())
		}
		list = append(list, p...)
	}
	if len(list) == 0 {
		return nil, errors.New("no destinations: pass --to and/or --preset")
	}

	originTokens := map[string]bool{}
	for _, tok := range strings.Split(from, ",") {
		originTokens[strings.ToLower(strings.TrimSpace(tok))] = true
	}

	seen := map[string]bool{}
	out := make([]string, 0, len(list))
	for _, d := range list {
		key := strings.ToLower(d)
		if seen[key] || originTokens[key] {
			continue
		}
		seen[key] = true
		out = append(out, d)
	}
	if len(out) == 0 {
		return nil, errors.New("no destinations left after removing the origin")
	}
	return out, nil
}

type dealParams struct {
	srcCities, srcAirports []string
	start, end             time.Time
	duration               int
	opts                   flights.Options
	dests                  []string
	concurrency            int
}

// searchDeals runs one price-graph search per destination (bounded by
// p.concurrency) and returns the cheapest round-trip for each, plus a map of
// destination -> error for those that failed or had no offers.
func searchDeals(ctx context.Context, sess *flights.BrowserSession, p dealParams) ([]deal, map[string]string) {
	var (
		mu       sync.Mutex
		deals    []deal
		failures = map[string]string{}
		wg       sync.WaitGroup
		sem      = make(chan struct{}, p.concurrency)
	)
	currencyCode := p.opts.Currency.String()

	for _, dest := range p.dests {
		wg.Add(1)
		go func(dest string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			dstCities, dstAirports := splitLocations(dest)
			offers, err := sess.GetPriceGraph(ctx, flights.PriceGraphArgs{
				RangeStartDate: p.start,
				RangeEndDate:   p.end,
				TripLength:     p.duration,
				SrcCities:      p.srcCities,
				SrcAirports:    p.srcAirports,
				DstCities:      dstCities,
				DstAirports:    dstAirports,
				Options:        p.opts,
			})

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failures[dest] = err.Error()
				return
			}
			best, ok := cheapestOffer(offers)
			if !ok {
				failures[dest] = "no offers with a price"
				return
			}
			deals = append(deals, deal{
				Dest:     dest,
				Price:    best.Price,
				Depart:   best.StartDate.Format("2006-01-02"),
				Return:   best.ReturnDate.Format("2006-01-02"),
				Currency: currencyCode,
			})
		}(dest)
	}
	wg.Wait()
	return deals, failures
}

func cheapestOffer(offers []flights.Offer) (flights.Offer, bool) {
	var best flights.Offer
	found := false
	for _, o := range offers {
		if o.Price <= 0 {
			continue
		}
		if !found || o.Price < best.Price {
			best = o
			found = true
		}
	}
	return best, found
}

func writeDealsText(w io.Writer, c commonOpts, start, end time.Time, duration, nDests, totalWithOffers int, deals []deal, failures map[string]string) {
	shown := ""
	if len(deals) < totalWithOffers {
		shown = fmt.Sprintf(" (showing %d)", len(deals))
	}
	fmt.Fprintf(w, "%s -> %d destinations  |  %s..%s  |  %d-day trip  |  %d with offers%s\n",
		c.from, nDests, start.Format("2006-01-02"), end.Format("2006-01-02"), duration, totalWithOffers, shown)
	fmt.Fprintf(w, "%-14s  %12s  %-12s  %-12s\n", "DESTINATION", "PRICE", "DEPART", "RETURN")
	fmt.Fprintf(w, "%s\n", strings.Repeat("-", 56))
	for _, d := range deals {
		fmt.Fprintf(w, "%-14s  %10.2f %s  %-12s  %-12s\n",
			d.Dest, d.Price, d.Currency, d.Depart, d.Return)
	}
	if len(failures) > 0 {
		names := make([]string, 0, len(failures))
		for k := range failures {
			names = append(names, k)
		}
		sort.Strings(names)
		fmt.Fprintf(w, "\n%d destination(s) returned no offers: %s\n", len(names), strings.Join(names, ", "))
	}
}

func writeDealsJSON(w io.Writer, c commonOpts, start, end time.Time, duration, nDests, totalWithOffers int, deals []deal, failures map[string]string) error {
	type failure struct {
		Dest   string `json:"dest"`
		Reason string `json:"reason"`
	}
	out := struct {
		Type            string    `json:"type"`
		Query           queryJSON `json:"query"`
		DestinationsAll int       `json:"destinations_searched"`
		TotalWithOffers int       `json:"total_with_offers"`
		Count           int       `json:"count"`
		Deals           []deal    `json:"deals"`
		Failures        []failure `json:"failures,omitempty"`
	}{
		Type:            "deals",
		Query:           queryFromCommon(c, start, end, duration, ""),
		DestinationsAll: nDests,
		TotalWithOffers: totalWithOffers,
		Count:           len(deals),
		Deals:           deals,
	}
	names := make([]string, 0, len(failures))
	for k := range failures {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, n := range names {
		out.Failures = append(out.Failures, failure{Dest: n, Reason: failures[n]})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
