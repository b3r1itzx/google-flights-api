package flights

import (
	"context"
	"fmt"
)

// GetHotelPriceGraph sweeps check-in dates across the given range (stepping by
// StepDays), querying a fixed-length stay for each, and returns the cheapest
// star-matching hotel per check-in date. This is the hotel analogue of
// [Session.GetPriceGraph] and answers "which dates are cheapest to stay".
//
// It performs one request per sampled date sequentially; callers sweeping wide
// ranges should expect proportional latency.
func (s *Session) GetHotelPriceGraph(ctx context.Context, args HotelPriceGraphArgs) ([]HotelDateOffer, error) {
	if err := args.Validate(); err != nil {
		return nil, err
	}

	var offers []HotelDateOffer
	for ci := args.RangeStartDate; !ci.After(args.RangeEndDate); ci = ci.AddDate(0, 0, args.StepDays) {
		co := ci.AddDate(0, 0, args.Nights)

		hotels, err := s.GetHotelOffers(ctx, HotelArgs{
			Location:     args.Location,
			CheckInDate:  ci,
			CheckOutDate: co,
			HotelOptions: args.HotelOptions,
		})
		if err != nil {
			return nil, fmt.Errorf("hotel offers for %s: %v", ci.Format("2006-01-02"), err)
		}

		cheapest, ok := cheapestPriced(hotels)
		if !ok {
			continue
		}
		offers = append(offers, HotelDateOffer{
			CheckInDate:  ci,
			CheckOutDate: co,
			Hotel:        cheapest,
		})
	}
	return offers, nil
}

// cheapestPriced returns the lowest-priced hotel with a non-zero price.
func cheapestPriced(hotels []Hotel) (Hotel, bool) {
	var best Hotel
	found := false
	for _, h := range hotels {
		if h.Price <= 0 {
			continue
		}
		if !found || h.Price < best.Price {
			best = h
			found = true
		}
	}
	return best, found
}
