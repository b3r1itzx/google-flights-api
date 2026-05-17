// This example gets best offers in the provided date range and print the cheapest one.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/b3r1itzx/google-flights-api/flights"
	"golang.org/x/text/currency"
	"golang.org/x/text/language"
)

func getCheapesOffer(
	rangeStartDate, rangeEndDate time.Time,
	tripLength int,
	srcAirport, dstAirport string,
	lang language.Tag,
) {
	//session, err := flights.NewWithProxy("http://geonode_4IiA2gxjGM-type-residential:51749aea-832a-4939-abea-10d35cb9e159@us.proxy.geonode.io:9000")
	session, err := flights.New()
	if err != nil {
		log.Fatal(err)
	}

	options := flights.Options{
		Travelers: flights.Travelers{Adults: 1},
		Currency:  currency.PLN,
		Stops:     flights.AnyStops,
		Class:     flights.Economy,
		TripType:  flights.RoundTrip,
		Lang:      lang,
	}

	offers, err := session.GetPriceGraph(
		context.Background(),
		flights.PriceGraphArgs{
			RangeStartDate: rangeStartDate,
			RangeEndDate:   rangeEndDate,
			TripLength:     tripLength,
			SrcAirports:    []string{srcAirport},
			DstAirports:    []string{dstAirport},
			Options:        options,
		},
	)
	if err != nil {
		log.Fatal(err)
	}

	var bestOffer flights.Offer
	for _, o := range offers {
		if o.Price != 0 && (bestOffer.Price == 0 || o.Price < bestOffer.Price) {
			bestOffer = o
		}
	}

	fmt.Printf("%s %s\n", bestOffer.StartDate, bestOffer.ReturnDate)
	fmt.Printf("price %d\n", int(bestOffer.Price))
	url, err := session.SerializeURL(
		context.Background(),
		flights.Args{
			Date:        bestOffer.StartDate,
			ReturnDate:  bestOffer.ReturnDate,
			SrcAirports: []string{srcAirport},
			DstAirports: []string{dstAirport},
			Options:     options,
		},
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(url)
}

func main() {
	startDate := time.Date(2025, time.December, 10, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2025, time.December, 24, 0, 0, 0, 0, time.UTC)

	getCheapesOffer(
		startDate,
		endDate,
		7,
		"JFK",
		"ORF",
		language.English,
	)
}
