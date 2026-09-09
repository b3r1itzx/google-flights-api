package main

import (
	"encoding/json"
	"io"
	"sync"
)

// ndjsonWriter emits newline-delimited JSON — one object per line, each written
// (and flushed) as it happens so a consumer sees results incrementally rather
// than waiting for the whole run. It is safe for concurrent use: deals finishes
// destinations on many goroutines and emits each as it lands.
type ndjsonWriter struct {
	mu  sync.Mutex
	w   io.Writer
	err error // first write error, sticky
}

func newNDJSON(w io.Writer) *ndjsonWriter { return &ndjsonWriter{w: w} }

// emit writes one JSON object followed by a newline. Marshalling happens
// outside the lock; only the write is serialized.
func (n *ndjsonWriter) emit(obj any) {
	b, err := json.Marshal(obj)
	if err != nil {
		n.mu.Lock()
		if n.err == nil {
			n.err = err
		}
		n.mu.Unlock()
		return
	}
	b = append(b, '\n')
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.err != nil {
		return
	}
	_, n.err = n.w.Write(b)
}

// Line types on the wire. Each carries a distinct "type" so a consumer can
// dispatch, and every stream ends with exactly one "done".

type streamMeta struct {
	Type     string `json:"type"` // "meta"
	Command  string `json:"command"`
	From     string `json:"from,omitempty"`
	To       string `json:"to,omitempty"`
	Depart   string `json:"depart,omitempty"`
	Return   string `json:"return,omitempty"`
	Start    string `json:"range_start,omitempty"`
	End      string `json:"range_end,omitempty"`
	Duration int    `json:"duration_days,omitempty"`
	Count    int    `json:"destinations,omitempty"`
	Currency string `json:"currency,omitempty"`
	URL      string `json:"url,omitempty"` // offers: the Google Flights booking deep link
}

type streamDeal struct {
	Type     string  `json:"type"` // "deal"
	Dest     string  `json:"dest"`
	Price    float64 `json:"price"`
	Typical  float64 `json:"typical"`
	Discount float64 `json:"discount"`
	Depart   string  `json:"depart"`
	Return   string  `json:"return"`
	Currency string  `json:"currency"`
}

type streamFare struct {
	Type     string  `json:"type"` // "fare"
	Depart   string  `json:"depart"`
	Return   string  `json:"return"`
	Price    float64 `json:"price"`
	Currency string  `json:"currency"`
}

type streamPriceRange struct {
	Type     string  `json:"type"` // "price_range"
	Low      float64 `json:"low"`
	High     float64 `json:"high"`
	Currency string  `json:"currency"`
}

type streamFailure struct {
	Type   string `json:"type"` // "failure"
	Dest   string `json:"dest"`
	Reason string `json:"reason"`
}

type streamDone struct {
	Type     string `json:"type"` // "done"
	Count    int    `json:"count"`
	Failures int    `json:"failures"`
}
