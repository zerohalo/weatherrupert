package renderer

import (
	"time"

	"git.sr.ht/~sbinet/gg"
)

// Holiday represents a recognized holiday with its icon draw function.
type Holiday struct {
	Name string
	Draw func(dc *gg.Context, cx, cy, size float64)
}

// ActiveHolidays returns the holidays active on the given date.
// Multiple holidays can be active on the same day.
func ActiveHolidays(t time.Time) []Holiday {
	month := t.Month()
	day := t.Day()

	var holidays []Holiday

	// Fixed-date holidays.
	switch {
	case month == time.January && day == 1:
		holidays = append(holidays, Holiday{"New Year's Day", drawHolidayChampagne})
	case month == time.February && day == 14:
		holidays = append(holidays, Holiday{"Valentine's Day", drawHolidayHeart})
	case month == time.March && day == 17:
		holidays = append(holidays, Holiday{"St. Patrick's Day", drawHolidayShamrock})
	case month == time.June && day == 19:
		holidays = append(holidays, Holiday{"Juneteenth", drawHolidayJuneteenth})
	case month == time.July && day == 4:
		holidays = append(holidays, Holiday{"Independence Day", drawHolidayFirework})
	case month == time.October && day == 31:
		holidays = append(holidays, Holiday{"Halloween", drawHolidayPumpkin})
	case month == time.November && day == 11:
		holidays = append(holidays, Holiday{"Veterans Day", drawHolidayMedal})
	case month == time.December && day == 25:
		holidays = append(holidays, Holiday{"Christmas", drawHolidayTree})
	}

	// Variable-date holidays.
	if month == time.January && nthWeekday(t, time.Monday, 3) {
		holidays = append(holidays, Holiday{"MLK Day", drawHolidayDove})
	}
	if month == time.February && nthWeekday(t, time.Monday, 3) {
		holidays = append(holidays, Holiday{"Presidents' Day", drawHolidayShield})
	}
	if isEaster(t) {
		holidays = append(holidays, Holiday{"Easter", drawHolidayEgg})
	}
	if month == time.May && lastWeekday(t, time.Monday) {
		holidays = append(holidays, Holiday{"Memorial Day", drawHolidayPoppy})
	}
	if month == time.September && nthWeekday(t, time.Monday, 1) {
		holidays = append(holidays, Holiday{"Labor Day", drawHolidayTools})
	}
	if month == time.October && nthWeekday(t, time.Monday, 2) {
		holidays = append(holidays, Holiday{"Indigenous Peoples'", drawHolidayFeather})
	}
	if month == time.November && nthWeekday(t, time.Thursday, 4) {
		holidays = append(holidays, Holiday{"Thanksgiving", drawHolidayTurkey})
	}

	return holidays
}

// nthWeekday returns true if t is the nth occurrence of the given weekday
// in its month (1-indexed).
func nthWeekday(t time.Time, weekday time.Weekday, n int) bool {
	if t.Weekday() != weekday {
		return false
	}
	return (t.Day()-1)/7+1 == n
}

// lastWeekday returns true if t is the last occurrence of the given weekday
// in its month.
func lastWeekday(t time.Time, weekday time.Weekday) bool {
	if t.Weekday() != weekday {
		return false
	}
	// Check if there's another occurrence of the same weekday this month.
	next := t.AddDate(0, 0, 7)
	return next.Month() != t.Month()
}

// isEaster returns true if t is Easter Sunday.
// Uses the anonymous Gregorian algorithm.
func isEaster(t time.Time) bool {
	year := t.Year()
	a := year % 19
	b := year / 100
	c := year % 100
	d := b / 4
	e := b % 4
	f := (b + 8) / 25
	g := (b - f + 1) / 3
	h := (19*a + b - d - g + 15) % 30
	i := c / 4
	k := c % 4
	l := (32 + 2*e + 2*i - h - k) % 7
	m := (a + 11*h + 22*l) / 451
	month := (h + l - 7*m + 114) / 31
	day := (h+l-7*m+114)%31 + 1

	return t.Month() == time.Month(month) && t.Day() == day
}
