package flights

import (
	"fmt"
	"time"

	"golang.org/x/text/currency"
	"golang.org/x/text/language"
)

// Hotel describes a single hotel offer returned by [Session.GetHotelOffers].
type Hotel struct {
	Name         string    // hotel name
	Price        float64   // nightly price in the requested currency (0 if unavailable)
	BasePrice    float64   // "usual"/strikethrough nightly price, when a deal is shown (0 otherwise)
	Currency     string    // ISO 4217 currency code of Price
	Stars        int       // hotel class (1-5); 0 when unrated (e.g. vacation rentals)
	Rating       float64   // average guest review score (0-5; 0 if none)
	ReviewCount  int       // number of guest reviews
	Description  string    // short editorial description
	Latitude     float64   // location latitude
	Longitude    float64   // location longitude
	ID           string    // Google hotel cluster ID
	CheckInDate  time.Time // check-in date this price is for
	CheckOutDate time.Time // check-out date this price is for
}

// HotelOptions contains common arguments for hotel searches.
type HotelOptions struct {
	MinStars int           // minimum hotel class to include (0 = no minimum)
	MaxStars int           // maximum hotel class to include (0 = no maximum)
	Currency currency.Unit // price currency
	Lang     language.Tag  // language for place-name resolution and text
}

// HotelOptionsDefault returns sensible defaults: no star filter, USD, English.
func HotelOptionsDefault() HotelOptions {
	return HotelOptions{
		MinStars: 0,
		MaxStars: 0,
		Currency: currency.USD,
		Lang:     language.English,
	}
}

func (o HotelOptions) currencyCode() string {
	c := o.Currency.String()
	if c == "XXX" || c == "" {
		return "USD"
	}
	return c
}

// starOK reports whether a hotel of the given class passes the star filter.
// Unrated hotels (stars == 0) are kept only when no minimum is set.
func (o HotelOptions) starOK(stars int) bool {
	if o.MinStars > 0 {
		if stars == 0 || stars < o.MinStars {
			return false
		}
	}
	if o.MaxStars > 0 && stars > o.MaxStars {
		return false
	}
	return true
}

// HotelArgs are the arguments for [Session.GetHotelOffers].
type HotelArgs struct {
	Location     string    // city, place, or ZIP code to search (e.g. "New York" or "10065")
	CheckInDate  time.Time // start of stay
	CheckOutDate time.Time // end of stay
	HotelOptions
}

func validateHotelStay(checkIn, checkOut time.Time) error {
	now := timeNow().Truncate(24 * time.Hour)
	if checkOut.Before(checkIn) || checkOut.Equal(checkIn) {
		return fmt.Errorf("checkOutDate must be after checkInDate")
	}
	if checkIn.Before(now) {
		return fmt.Errorf("checkInDate is before today's date")
	}
	return nil
}

// Validate checks the hotel offer arguments.
func (a *HotelArgs) Validate() error {
	if a.Location == "" {
		return fmt.Errorf("location is required")
	}
	a.CheckInDate = truncateToDay(a.CheckInDate)
	a.CheckOutDate = truncateToDay(a.CheckOutDate)
	return validateHotelStay(a.CheckInDate, a.CheckOutDate)
}

// HotelPriceGraphArgs are the arguments for [Session.GetHotelPriceGraph].
type HotelPriceGraphArgs struct {
	Location       string    // city, place, or ZIP to search
	RangeStartDate time.Time // earliest check-in date to sample
	RangeEndDate   time.Time // latest check-in date to sample
	Nights         int       // length of stay in nights
	StepDays       int       // days between sampled check-in dates (default 1)
	HotelOptions
}

// HotelDateOffer is the cheapest qualifying stay for one check-in date,
// returned by [Session.GetHotelPriceGraph].
type HotelDateOffer struct {
	CheckInDate  time.Time
	CheckOutDate time.Time
	Hotel        Hotel // the cheapest hotel matching the star filter for this window
}

// Validate checks the hotel price-graph arguments.
func (a *HotelPriceGraphArgs) Validate() error {
	if a.Location == "" {
		return fmt.Errorf("location is required")
	}
	if a.Nights < 1 {
		return fmt.Errorf("nights must be at least 1")
	}
	if a.StepDays < 1 {
		a.StepDays = 1
	}
	a.RangeStartDate = truncateToDay(a.RangeStartDate)
	a.RangeEndDate = truncateToDay(a.RangeEndDate)
	now := timeNow().Truncate(24 * time.Hour)
	if a.RangeEndDate.Before(a.RangeStartDate) {
		return fmt.Errorf("rangeEndDate is before rangeStartDate")
	}
	if a.RangeStartDate.Before(now) {
		return fmt.Errorf("rangeStartDate is before today's date")
	}
	return nil
}
