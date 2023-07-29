package main

import (
	"fmt"
	"log"
	"time"

	"github.com/krisukox/google-flights-api/flights"
	"golang.org/x/text/currency"
	"golang.org/x/text/language"
)

func getBestOffer(rangeStartDate, rangeEndDate time.Time, tripLength int, srcCity, dstCity string, lang language.Tag) {
	session := flights.New()
	var bestOffer flights.Offer

	args := flights.Args{
		Adults:   1,
		Curr:     currency.PLN,
		Stops:    flights.AnyStops,
		Class:    flights.Economy,
		TripType: flights.RoundTrip,
		Lang:     lang,
	}

	offers, err := session.GetPriceGraph(
		flights.PriceGraphArgs{
			RangeStartDate: rangeStartDate,
			RangeEndDate:   rangeEndDate,
			TripLength:     tripLength,
			SrcCities:      []string{srcCity},
			DstCities:      []string{dstCity},
			Args:           args,
		},
	)
	if err != nil {
		log.Fatal(err)
	}

	for _, o := range offers {
		if bestOffer.Price == 0 || o.Price < bestOffer.Price {
			bestOffer = o
		}
	}

	fmt.Printf("%s %s\n", bestOffer.StartDate, bestOffer.ReturnDate)
	fmt.Printf("price %d\n", int(bestOffer.Price))
	url, err := session.SerializeUrl(
		flights.UrlArgs{
			Date:       bestOffer.StartDate,
			ReturnDate: bestOffer.ReturnDate,
			SrcCities:  []string{srcCity},
			DstCities:  []string{dstCity},
			Args:       args,
		},
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(url)
}

func main() {
	getBestOffer(
		time.Now().AddDate(0, 0, 60),
		time.Now().AddDate(0, 0, 90),
		2,
		"Warsaw",
		"Athens",
		language.English,
	)
}
