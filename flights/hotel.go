package flights

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/go-retryablehttp"
)

// hotelDateTriple renders a date as the [year, month, day] form Google expects
// (month is 1-indexed: June -> 6).
func hotelDateTriple(t time.Time) []interface{} {
	return []interface{}{t.Year(), int(t.Month()), t.Day()}
}

// buildHotelReqData builds the f.req inner payload for the AtySUc RPC. The
// location feature ID can be left null — Google resolves the place from the
// query string, so a single call suffices.
func buildHotelReqData(args HotelArgs) (string, error) {
	query := "hotels in " + args.Location
	nights := int(args.CheckOutDate.Sub(args.CheckInDate).Hours() / 24)

	inner := []interface{}{
		query,
		[]interface{}{
			1,
			[]interface{}{[]interface{}{[]interface{}{}, []interface{}{}}, 0}, // star filter slot (server-side filtering unreliable; we filter client-side)
			[]interface{}{
				[]interface{}{nil, []interface{}{[]interface{}{nil, nil, nil, nil, nil, nil, args.Location}}},
				[]interface{}{nil, []interface{}{hotelDateTriple(args.CheckInDate), hotelDateTriple(args.CheckOutDate), nights}, nil, nil, nil, []interface{}{nil, 0}},
			},
			nil,
			[]interface{}{[]interface{}{nil, nil, nil, nil, nil, nil, args.currencyCode()}},
		},
		[]interface{}{nil, nil, nil, nil, nil, nil, 13},
		nil,
		1,
	}

	innerJSON, err := json.Marshal(inner)
	if err != nil {
		return "", fmt.Errorf("could not marshal hotel request: %v", err)
	}

	envelope := [][][]interface{}{{{"AtySUc", string(innerJSON), nil, "1"}}}
	envelopeJSON, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("could not marshal hotel envelope: %v", err)
	}

	return url.QueryEscape(string(envelopeJSON)), nil
}

func (s *Session) doRequestHotels(ctx context.Context, args HotelArgs) (*http.Response, error) {
	reqURL := "https://www.google.com/_/TravelFrontendUi/data/batchexecute" +
		"?rpcids=AtySUc" +
		"&source-path=%2Ftravel%2Fsearch" +
		"&f.sid=" + url.QueryEscape(s.fsid) +
		"&bl=" + url.QueryEscape(s.bl) +
		"&hl=" + args.Lang.String() +
		"&soc-app=162&soc-platform=1&soc-device=1&_reqid=2181955&rt=c"

	reqData, err := buildHotelReqData(args)
	if err != nil {
		return nil, err
	}

	jsonBody := []byte(`f.req=` + reqData + `&at=&`)

	req, err := retryablehttp.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create hotel request: %v", err)
	}
	req.Header.Set("accept", `*/*`)
	req.Header.Set("content-type", `application/x-www-form-urlencoded;charset=UTF-8`)
	req.Header.Set("origin", `https://www.google.com`)
	req.Header.Set("x-same-domain", `1`)
	req.Header["cookie"] = s.cookies
	req.Header.Set("user-agent", `Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/113.0.0.0 Safari/537.36`)
	req.Header.Set("x-goog-ext-259736195-jspb",
		fmt.Sprintf(`["en-US","US","%s",2,null,[240],null,null,7,[]]`, args.currencyCode()))

	return s.client.Do(req)
}

// extractBatchPayload reads a batchexecute response body and returns the inner
// JSON payload string for the given rpcid. The wire format is an XSSI prefix
// (")]}'") followed by length-prefixed chunks; each chunk is a JSON array, and
// the data row looks like ["wrb.fr","<rpcid>","<payload-json-string>",...].
func extractBatchPayload(r io.Reader, rpcid string) (string, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	s := string(raw)
	if i := strings.IndexByte(s, '\n'); strings.HasPrefix(s, ")]}'") && i >= 0 {
		s = s[i+1:]
	}

	dec := json.NewDecoder(strings.NewReader(s))
	for {
		var chunk interface{}
		if err := dec.Decode(&chunk); err != nil {
			if err == io.EOF {
				break
			}
			// Length tokens between chunks are bare numbers; Decode handles them
			// as their own values, so a syntax error is genuinely unexpected.
			return "", fmt.Errorf("batch decode: %v", err)
		}
		if payload := findWrbFr(chunk, rpcid); payload != "" {
			return payload, nil
		}
	}
	return "", fmt.Errorf("rpcid %q not found in response", rpcid)
}

func findWrbFr(v interface{}, rpcid string) string {
	arr, ok := v.([]interface{})
	if !ok {
		return ""
	}
	if len(arr) >= 3 {
		if a0, ok := arr[0].(string); ok && a0 == "wrb.fr" {
			if a1, ok := arr[1].(string); ok && a1 == rpcid {
				if a2, ok := arr[2].(string); ok {
					return a2
				}
			}
		}
	}
	for _, e := range arr {
		if p := findWrbFr(e, rpcid); p != "" {
			return p
		}
	}
	return ""
}

// deepDecodeJSON recursively re-parses any string value that is itself a JSON
// array or object. Google's batchexecute responses embed field-keyed records as
// stringified JSON nested inside the outer payload.
func deepDecodeJSON(v interface{}, depth int) interface{} {
	if depth > 16 {
		return v
	}
	switch x := v.(type) {
	case string:
		s := strings.TrimSpace(x)
		if len(s) >= 2 && (s[0] == '[' || s[0] == '{') && (s[len(s)-1] == ']' || s[len(s)-1] == '}') {
			var parsed interface{}
			if json.Unmarshal([]byte(s), &parsed) == nil {
				return deepDecodeJSON(parsed, depth+1)
			}
		}
		return x
	case []interface{}:
		for i := range x {
			x[i] = deepDecodeJSON(x[i], depth+1)
		}
		return x
	case map[string]interface{}:
		for k := range x {
			x[k] = deepDecodeJSON(x[k], depth+1)
		}
		return x
	}
	return v
}

// collectHotelRecords walks the decoded tree and returns every hotel record.
// Three encodings exist: the direct RPC keys wrapped records
// ([[...fields...]]) under "397419284"; the search page's own RPC keys bare
// field lists under "179305178" for the organic listing, and under
// "441552390" for the entity match — the specific hotel a name-like query
// resolved to. Bare records are wrapped here so all come out in the same
// [[...fields...]] shape.
func collectHotelRecords(v interface{}, out *[]interface{}) {
	switch x := v.(type) {
	case map[string]interface{}:
		for k, val := range x {
			switch k {
			case "397419284":
				*out = append(*out, val)
			case "179305178", "441552390":
				*out = append(*out, []interface{}{val})
			}
			collectHotelRecords(val, out)
		}
	case []interface{}:
		for _, e := range x {
			collectHotelRecords(e, out)
		}
	}
}

func asFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// findPriceNode searches a hotel record for the nightly-price node, which has
// the shape ["$99", "$113", 98.64, null, 99] — [display, base, exactFloat, _,
// roundedInt]. The display strings carry whatever currency symbol the search
// used ("$99", "€241", "PLN 358", ...) and base is null when no deal is shown.
// Returns the exact nightly price and the "usual"/strikethrough base price.
func findPriceNode(v interface{}) (price, base float64, found bool) {
	arr, ok := v.([]interface{})
	if !ok {
		return 0, 0, false
	}
	if len(arr) >= 5 {
		s0, ok0 := arr[0].(string)
		exact, okExact := asFloat(arr[2])
		rounded, okRounded := asFloat(arr[4])
		// the rounded price mirroring the exact one is what distinguishes
		// this node from other [string, _, number, _, number] shapes
		if ok0 && okExact && okRounded && exact > 0 &&
			parseMoney(s0) > 0 && math.Abs(exact-rounded) <= 1 {
			if baseStr, ok := arr[1].(string); ok {
				base = parseMoney(baseStr)
			}
			return exact, base, true
		}
	}
	for _, e := range arr {
		if p, b, f := findPriceNode(e); f {
			return p, b, f
		}
	}
	return 0, 0, false
}

// parseMoney extracts the numeric amount from a display price like "$1,234",
// "€241", or "PLN 358". Returns 0 on failure.
func parseMoney(s string) float64 {
	digits := strings.Map(func(r rune) rune {
		if (r >= '0' && r <= '9') || r == '.' {
			return r
		}
		return -1
	}, strings.ReplaceAll(s, ",", ""))
	if f, err := strconv.ParseFloat(digits, 64); err == nil {
		return f
	}
	return 0
}

func parseStars(v interface{}) int {
	arr, ok := v.([]interface{})
	if !ok || len(arr) == 0 {
		return 0
	}
	s, ok := arr[0].(string)
	if !ok {
		return 0
	}
	// format: "3-star hotel"
	if i := strings.Index(s, "-star"); i > 0 {
		if n, err := strconv.Atoi(strings.TrimSpace(s[:i])); err == nil {
			return n
		}
	}
	return 0
}

// stayDates extracts the check-in/check-out dates Google echoed inside a
// record's price block ([6][1][4] = [[y,m,d],[y,m,d],nights,...]). These are
// the dates the returned price is actually for — when the request dates were
// not applied (e.g. a ts URL param regression), they reveal it.
func stayDates(v interface{}) (ci, co time.Time, ok bool) {
	blk, ok1 := v.([]interface{})
	if !ok1 || len(blk) < 2 {
		return ci, co, false
	}
	inner, ok2 := blk[1].([]interface{})
	if !ok2 || len(inner) < 5 {
		return ci, co, false
	}
	stay, ok3 := inner[4].([]interface{})
	if !ok3 || len(stay) < 2 {
		return ci, co, false
	}
	toDate := func(v interface{}) (time.Time, bool) {
		t, ok := v.([]interface{})
		if !ok || len(t) < 3 {
			return time.Time{}, false
		}
		y, okY := asFloat(t[0])
		m, okM := asFloat(t[1])
		d, okD := asFloat(t[2])
		if !okY || !okM || !okD {
			return time.Time{}, false
		}
		return time.Date(int(y), time.Month(m), int(d), 0, 0, 0, 0, time.UTC), true
	}
	ci, okCI := toDate(stay[0])
	co, okCO := toDate(stay[1])
	return ci, co, okCI && okCO
}

func parseHotelRecord(rw interface{}, args HotelArgs) (Hotel, bool) {
	// records are wrapped: [ [ ...fields... ] ]
	wrap, ok := rw.([]interface{})
	if !ok || len(wrap) == 0 {
		return Hotel{}, false
	}
	r, ok := wrap[0].([]interface{})
	if !ok || len(r) < 2 {
		return Hotel{}, false
	}

	h := Hotel{
		Currency:     args.currencyCode(),
		CheckInDate:  args.CheckInDate,
		CheckOutDate: args.CheckOutDate,
	}
	if name, ok := r[1].(string); ok {
		h.Name = name
	}
	if h.Name == "" {
		return Hotel{}, false
	}

	// stars at [3] = ["N-star hotel", N]
	if len(r) > 3 {
		h.Stars = parseStars(r[3])
	}
	// location at [2][0] = [lat, lng]
	if len(r) > 2 {
		if loc, ok := r[2].([]interface{}); ok && len(loc) > 0 {
			if ll, ok := loc[0].([]interface{}); ok && len(ll) >= 2 {
				h.Latitude, _ = asFloat(ll[0])
				h.Longitude, _ = asFloat(ll[1])
			}
		}
	}
	// rating at [7][0] = [rating, reviewCount]
	if len(r) > 7 {
		if rt, ok := r[7].([]interface{}); ok && len(rt) > 0 {
			if rr, ok := rt[0].([]interface{}); ok && len(rr) >= 2 {
				h.Rating, _ = asFloat(rr[0])
				if c, ok := asFloat(rr[1]); ok {
					h.ReviewCount = int(c)
				}
			}
		}
	}
	// description at [11] = [text]
	if len(r) > 11 {
		if d, ok := r[11].([]interface{}); ok && len(d) > 0 {
			if txt, ok := d[0].(string); ok {
				h.Description = txt
			}
		}
	}
	// cluster ID at [25]
	if len(r) > 25 {
		if id, ok := r[25].(string); ok {
			h.ID = id
		}
	}
	// price node (search the record subtree)
	if len(r) > 6 {
		if p, b, ok := findPriceNode(r[6]); ok {
			h.Price = p
			h.BasePrice = b
		}
		// prefer the stay dates Google echoed next to the price — they are
		// what the price is actually for
		if ci, co, ok := stayDates(r[6]); ok {
			h.CheckInDate = ci
			h.CheckOutDate = co
		}
	}
	if h.Price == 0 {
		if p, b, ok := findPriceNode(r); ok {
			h.Price = p
			h.BasePrice = b
		}
	}

	return h, true
}

// GetHotelOffers searches Google Hotels for the given location and stay dates,
// returning offers sorted by nightly price ascending. Results are filtered by
// the star range in [HotelArgs.HotelOptions] (applied client-side).
func (s *Session) GetHotelOffers(ctx context.Context, args HotelArgs) ([]Hotel, error) {
	if err := args.Validate(); err != nil {
		return nil, err
	}

	resp, err := s.doRequestHotels(ctx, args)
	if err != nil {
		return nil, fmt.Errorf("failed to do hotel request: %v", err)
	}
	defer resp.Body.Close()

	return parseHotelOffers(resp.Body, args)
}

// parseHotelOffers decodes an AtySUc batchexecute response body into hotel
// offers, filtered by the star range and sorted by nightly price ascending.
func parseHotelOffers(r io.Reader, args HotelArgs) ([]Hotel, error) {
	payload, err := extractBatchPayload(r, "AtySUc")
	if err != nil {
		return nil, fmt.Errorf("hotel response: %v", err)
	}

	var root interface{}
	if err := json.Unmarshal([]byte(payload), &root); err != nil {
		return nil, fmt.Errorf("hotel decode error: %v", err)
	}
	root = deepDecodeJSON(root, 0)

	var records []interface{}
	collectHotelRecords(root, &records)

	hotels := make([]Hotel, 0, len(records))
	seen := map[string]bool{}
	for _, rec := range records {
		h, ok := parseHotelRecord(rec, args)
		if !ok || seen[h.Name] {
			continue
		}
		if !args.starOK(h.Stars) || !args.nameOK(h.Name) {
			continue
		}
		seen[h.Name] = true
		hotels = append(hotels, h)
	}

	sortSlice(hotels, func(lv, rv Hotel) bool {
		if (lv.Price == 0) != (rv.Price == 0) {
			return rv.Price == 0 // hotels with a price first
		}
		return lv.Price < rv.Price
	})
	return hotels, nil
}
