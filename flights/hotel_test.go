package flights

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/currency"
	"golang.org/x/text/language"
)

func TestBuildHotelReqData(t *testing.T) {
	args := HotelArgs{
		Location:     "New York",
		CheckInDate:  time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC),
		CheckOutDate: time.Date(2026, time.June, 11, 0, 0, 0, 0, time.UTC),
		HotelOptions: HotelOptions{Currency: currency.USD, Lang: language.English},
	}
	enc, err := buildHotelReqData(args)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := url.QueryUnescape(enc)
	if err != nil {
		t.Fatal(err)
	}

	// envelope -> [[["AtySUc", "<inner json>", null, "1"]]]
	var envelope [][][]interface{}
	if err := json.Unmarshal([]byte(decoded), &envelope); err != nil {
		t.Fatalf("envelope unmarshal: %v", err)
	}
	if envelope[0][0][0] != "AtySUc" {
		t.Fatalf("rpcid: got %v want AtySUc", envelope[0][0][0])
	}
	inner, _ := envelope[0][0][1].(string)

	if !strings.Contains(inner, "hotels in New York") {
		t.Errorf("inner missing query: %s", inner)
	}
	// month must be 1-indexed: June -> 6
	if !strings.Contains(inner, "[2026,6,1]") || !strings.Contains(inner, "[2026,6,11]") {
		t.Errorf("inner missing 1-indexed dates: %s", inner)
	}
	if !strings.Contains(inner, `"USD"`) {
		t.Errorf("inner missing currency: %s", inner)
	}
	// 10 nights between Jun 1 and Jun 11
	if !strings.Contains(inner, ",10]") {
		t.Errorf("inner missing nights count: %s", inner)
	}
}

func TestParseStars(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{`["3-star hotel",3]`, 3},
		{`["5-star hotel",5]`, 5},
		{`["Vacation rental"]`, 0},
		{`[]`, 0},
		{`["4-star hotel - x",4]`, 4},
	}
	for _, c := range cases {
		if got := parseStars(toIface(c.in)); got != c.want {
			t.Errorf("parseStars(%s) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestFindPriceNode(t *testing.T) {
	// record fragment with the price node nested inside
	rec := toIface(`[null,[[99,0],null,null,"USD",[[2026,6,1],[2026,6,11],10]],[null,["$99","$113",98.64,null,99]]]`)
	price, base, ok := findPriceNode(rec)
	if !ok {
		t.Fatal("expected to find price node")
	}
	if price != 98.64 {
		t.Errorf("price = %v, want 98.64", price)
	}
	if base != 113 {
		t.Errorf("base = %v, want 113", base)
	}
}

func TestDeepDecodeJSON(t *testing.T) {
	// a string that is itself JSON should be expanded
	in := toIface(`["outer",{"397419284":"[[1,2,3]]"}]`)
	out := deepDecodeJSON(in, 0)
	arr := out.([]interface{})
	m := arr[1].(map[string]interface{})
	inner, ok := m["397419284"].([]interface{})
	if !ok {
		t.Fatalf("nested JSON string was not decoded: %T", m["397419284"])
	}
	if len(inner) != 1 {
		t.Errorf("decoded inner len = %d, want 1", len(inner))
	}
}

func TestParseHotelRecord(t *testing.T) {
	args := HotelArgs{
		Location:     "New York",
		CheckInDate:  time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC),
		CheckOutDate: time.Date(2026, time.June, 11, 0, 0, 0, 0, time.UTC),
		HotelOptions: HotelOptions{Currency: currency.USD},
	}
	// wrapped record: index 1 name, 2 location, 3 stars, 6 price subtree,
	// 7 rating, 11 description, 25 id
	rec := toIface(`[[null,"Test Hotel",[[40.5,-74.2]],["3-star hotel",3],null,null,` +
		`[null,null,[null,["$120","$140",119.5,null,120]]],` +
		`[[4.2,500]],null,null,null,["A nice place"],null,null,null,null,null,null,null,null,null,null,null,null,null,"99887766"]]`)
	h, ok := parseHotelRecord(rec, args)
	if !ok {
		t.Fatal("expected record to parse")
	}
	if h.Name != "Test Hotel" {
		t.Errorf("name = %q", h.Name)
	}
	if h.Stars != 3 {
		t.Errorf("stars = %d", h.Stars)
	}
	if h.Price != 119.5 {
		t.Errorf("price = %v", h.Price)
	}
	if h.Rating != 4.2 || h.ReviewCount != 500 {
		t.Errorf("rating = %v (%d)", h.Rating, h.ReviewCount)
	}
	if h.Description != "A nice place" {
		t.Errorf("description = %q", h.Description)
	}
	if h.ID != "99887766" {
		t.Errorf("id = %q", h.ID)
	}
	if h.Latitude != 40.5 || h.Longitude != -74.2 {
		t.Errorf("coords = %v,%v", h.Latitude, h.Longitude)
	}
}

func TestHotelOptionsStarOK(t *testing.T) {
	o := HotelOptions{MinStars: 4, MaxStars: 5}
	if !o.starOK(4) || !o.starOK(5) {
		t.Error("4 and 5 should pass")
	}
	if o.starOK(3) || o.starOK(0) {
		t.Error("3 and unrated should fail with min=4")
	}
	none := HotelOptions{}
	if !none.starOK(0) || !none.starOK(2) {
		t.Error("no filter should accept all")
	}
}

func TestHotelOptionsNameOK(t *testing.T) {
	o := HotelOptions{NameContains: "enchantment"}
	if !o.nameOK("Enchantment Resort") {
		t.Error("case-insensitive substring should match")
	}
	if o.nameOK("Amara Resort and Spa") {
		t.Error("non-matching name should be filtered")
	}
	if !(HotelOptions{}).nameOK("Anything") {
		t.Error("empty filter should match everything")
	}
}

func TestHotelArgsValidate(t *testing.T) {
	base := time.Now().AddDate(0, 1, 0)
	good := HotelArgs{Location: "X", CheckInDate: base, CheckOutDate: base.AddDate(0, 0, 3)}
	if err := good.Validate(); err != nil {
		t.Errorf("expected valid, got %v", err)
	}
	bad := HotelArgs{Location: "", CheckInDate: base, CheckOutDate: base.AddDate(0, 0, 3)}
	if err := bad.Validate(); err == nil {
		t.Error("expected error for empty location")
	}
	rev := HotelArgs{Location: "X", CheckInDate: base.AddDate(0, 0, 3), CheckOutDate: base}
	if err := rev.Validate(); err == nil {
		t.Error("expected error for checkout before checkin")
	}
}

// --- live tests (hit Google; skipped with -short) ---

func TestGetHotelOffersLive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live hotel test in -short mode")
	}
	session := newBrowserSessionForTest(t)
	hotels, err := session.GetHotelOffers(context.Background(), HotelArgs{
		Location:     "New York",
		CheckInDate:  time.Now().AddDate(0, 1, 0),
		CheckOutDate: time.Now().AddDate(0, 1, 10),
		HotelOptions: HotelOptions{Currency: currency.USD, Lang: language.English},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hotels) == 0 {
		t.Fatal("expected at least one hotel")
	}
	priced := 0
	wantCI := truncateToDay(time.Now().AddDate(0, 1, 0))
	for _, h := range hotels {
		if h.Name == "" {
			t.Error("hotel with empty name")
		}
		if h.Price > 0 {
			priced++
			// the dates echoed next to the price must be the requested stay —
			// otherwise Google priced a default stay and the ts URL param
			// encoding has regressed
			if !h.CheckInDate.Equal(wantCI) {
				t.Errorf("hotel %q priced for check-in %s, want %s (date encoding regressed?)",
					h.Name, h.CheckInDate.Format("2006-01-02"), wantCI.Format("2006-01-02"))
			}
		}
	}
	if priced == 0 {
		t.Error("expected at least one hotel with a price")
	}
}

func TestGetHotelOffersStarFilterLive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live hotel test in -short mode")
	}
	session := newBrowserSessionForTest(t)
	hotels, err := session.GetHotelOffers(context.Background(), HotelArgs{
		Location:     "New York",
		CheckInDate:  time.Now().AddDate(0, 1, 0),
		CheckOutDate: time.Now().AddDate(0, 1, 10),
		HotelOptions: HotelOptions{MinStars: 4, MaxStars: 5, Currency: currency.USD, Lang: language.English},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hotels {
		if h.Stars < 4 || h.Stars > 5 {
			t.Errorf("hotel %q has %d stars, outside 4-5 filter", h.Name, h.Stars)
		}
	}
}

func TestGetHotelPriceGraphLive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live hotel test in -short mode")
	}
	session := newBrowserSessionForTest(t)
	offers, err := session.GetHotelPriceGraph(context.Background(), HotelPriceGraphArgs{
		Location:       "New York",
		RangeStartDate: time.Now().AddDate(0, 1, 0),
		RangeEndDate:   time.Now().AddDate(0, 1, 6),
		Nights:         10,
		StepDays:       3,
		HotelOptions:   HotelOptions{Currency: currency.USD, Lang: language.English},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offers) == 0 {
		t.Fatal("expected at least one date offer")
	}
	for _, o := range offers {
		if o.Hotel.Price <= 0 {
			t.Errorf("offer for %s has no price", o.CheckInDate.Format("2006-01-02"))
		}
		if o.CheckOutDate.Sub(o.CheckInDate) != 10*24*time.Hour {
			t.Errorf("offer span is not 10 nights")
		}
	}
}

// toIface parses a JSON literal into interface{} for table-driven tests.
func toIface(s string) interface{} {
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		panic(err)
	}
	return v
}
