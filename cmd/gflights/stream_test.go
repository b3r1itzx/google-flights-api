package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

// decodeNDJSON splits a stream into one decoded map per line.
func decodeNDJSON(t *testing.T, s string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("line is not valid JSON: %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func TestNDJSONEmitConcurrent(t *testing.T) {
	var buf bytes.Buffer
	nd := newNDJSON(&buf)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			nd.emit(streamDeal{Type: "deal", Dest: "X", Price: float64(i)})
		}(i)
	}
	wg.Wait()
	if nd.err != nil {
		t.Fatal(nd.err)
	}
	lines := decodeNDJSON(t, buf.String())
	if len(lines) != 50 {
		t.Fatalf("got %d lines, want 50 (concurrent writes must not interleave)", len(lines))
	}
	for _, m := range lines {
		if m["type"] != "deal" {
			t.Errorf("bad line: %v", m)
		}
	}
}

func TestOfferLineCarriesURL(t *testing.T) {
	o := fixtureOffer()
	line := offerLine(o, testArgs(), "https://www.google.com/travel/flights/search?tfs=abc")
	if line["type"] != "offer" {
		t.Errorf("type = %v", line["type"])
	}
	if line["url"] != "https://www.google.com/travel/flights/search?tfs=abc" {
		t.Errorf("offer line missing booking url: %v", line["url"])
	}
	if line["price"] != 771.0 {
		t.Errorf("price = %v", line["price"])
	}
	// no url passed -> key omitted, not an empty string
	bare := offerLine(o, testArgs(), "")
	if _, ok := bare["url"]; ok {
		t.Errorf("empty url should be omitted, got %v", bare["url"])
	}
}

func TestStreamLineShapes(t *testing.T) {
	var buf bytes.Buffer
	nd := newNDJSON(&buf)
	nd.emit(streamMeta{Type: "meta", Command: "deals", From: "ORF", Count: 3, Currency: "USD"})
	nd.emit(streamDeal{Type: "deal", Dest: "ATL", Price: 80, Typical: 220, Discount: 63.6, Depart: "2026-11-03", Return: "2026-11-07", Currency: "USD"})
	nd.emit(streamFailure{Type: "failure", Dest: "BGI", Reason: "price graph button not found"})
	nd.emit(streamDone{Type: "done", Count: 1, Failures: 1})

	lines := decodeNDJSON(t, buf.String())
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4", len(lines))
	}
	if lines[0]["type"] != "meta" || lines[0]["from"] != "ORF" || lines[0]["destinations"] != float64(3) {
		t.Errorf("meta = %v", lines[0])
	}
	d := lines[1]
	if d["type"] != "deal" || d["dest"] != "ATL" || d["price"] != float64(80) || d["discount"] != 63.6 {
		t.Errorf("deal = %v", d)
	}
	if lines[2]["type"] != "failure" || lines[2]["dest"] != "BGI" {
		t.Errorf("failure = %v", lines[2])
	}
	done := lines[len(lines)-1]
	if done["type"] != "done" || done["count"] != float64(1) || done["failures"] != float64(1) {
		t.Errorf("done = %v", done)
	}
}
