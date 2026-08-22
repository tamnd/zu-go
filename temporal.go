package zu

/*
#include <zu.h>
*/
import "C"

import (
	"fmt"
	"time"
)

// A TemporalKind says which of the seven temporal types a value is.
// The unit follows the kind: days for a date, months for a year-month
// duration, nanoseconds for the other five.
type TemporalKind int32

// The seven kinds, with the same numbers as the C ABI.
const (
	KindDate              TemporalKind = C.ZU_TEMPORAL_DATE
	KindLocalTime         TemporalKind = C.ZU_TEMPORAL_LOCAL_TIME
	KindZonedTime         TemporalKind = C.ZU_TEMPORAL_ZONED_TIME
	KindLocalDateTime     TemporalKind = C.ZU_TEMPORAL_LOCAL_DATETIME
	KindZonedDateTime     TemporalKind = C.ZU_TEMPORAL_ZONED_DATETIME
	KindYearMonthDuration TemporalKind = C.ZU_TEMPORAL_DURATION_YEAR_MONTH
	KindDayTimeDuration   TemporalKind = C.ZU_TEMPORAL_DURATION_DAY_TIME
)

// The temporal types are seven Go types rather than one with a tag,
// because the thing a Go program does with a temporal value is switch
// on it, and a switch over seven types is the shape the language has.
// Each one converts to the standard library type that means the same
// thing, and none of them is that type outright: a date at midnight
// and a datetime at midnight are different values, and only one of
// them can be [time.Time] without lying about the other.
//
// A day-time duration is the exception and is a [time.Duration]
// directly, since both are a count of nanoseconds and neither carries
// anything the other cannot hold.

// A Date is a day, counted from 1970-01-01, with no time and no zone.
type Date struct {
	// Days is how many days after 1970-01-01 this is, negative for a
	// date before it.
	Days int32
}

// Time is the date at midnight UTC, which is the only instant a date
// can be turned into without inventing a zone.
func (d Date) Time() time.Time {
	return time.Unix(int64(d.Days)*86400, 0).UTC()
}

// String is the date in the spelling the language writes it in.
func (d Date) String() string {
	return d.Time().Format("2006-01-02")
}

// A LocalTime is a time of day with no date and no zone, as
// nanoseconds since midnight.
type LocalTime struct {
	// Nanos is how far past midnight this is, in nanoseconds.
	Nanos int64
}

// Duration is the time of day as the distance from midnight.
func (t LocalTime) Duration() time.Duration {
	return time.Duration(t.Nanos)
}

// String is the time in the spelling the language writes it in.
func (t LocalTime) String() string {
	return clock(t.Nanos)
}

// A ZonedTime is a time of day in a zone, as nanoseconds since
// midnight in the offset's own day and the offset from UTC in minutes,
// east positive.
type ZonedTime struct {
	// Nanos is how far past midnight this is in its own zone, in
	// nanoseconds, and not how far past midnight UTC.
	Nanos int64
	// Offset is minutes east of UTC, so 60 is an hour ahead and -300
	// is five hours behind.
	Offset int32
}

// Duration is the time of day as the distance from midnight in its own
// zone.
func (t ZonedTime) Duration() time.Duration {
	return time.Duration(t.Nanos)
}

// String is the time and its offset.
func (t ZonedTime) String() string {
	return clock(t.Nanos) + offsetText(t.Offset)
}

// A LocalDateTime is a date and a time with no zone, as nanoseconds
// since 1970-01-01T00:00:00.
type LocalDateTime struct {
	// Nanos is how far past 1970-01-01T00:00:00 this is, in
	// nanoseconds, read with no zone at all rather than with UTC.
	Nanos int64
}

// Time is the value read as if the zone were UTC, which is what a
// value with no zone has to be read as to become a [time.Time] at all.
func (t LocalDateTime) Time() time.Time {
	return time.Unix(0, t.Nanos).UTC()
}

// String is the datetime with no zone on the end, because it has none.
func (t LocalDateTime) String() string {
	return t.Time().Format("2006-01-02T15:04:05.999999999")
}

// A ZonedDateTime is an instant and the offset it was written with, as
// nanoseconds since the epoch in UTC and the offset from UTC in
// minutes, east positive. The instant is the value; the offset is how
// it was said.
type ZonedDateTime struct {
	// Nanos is the instant, as nanoseconds since the epoch in UTC.
	// Two values with the same Nanos are the same moment however
	// differently they were written.
	Nanos int64
	// Offset is minutes east of UTC, which is how the instant was
	// said rather than part of which instant it is.
	Offset int32
}

// Time is the instant in a fixed zone at this value's offset, so that
// printing it gives back the wall clock it was written with.
func (t ZonedDateTime) Time() time.Time {
	return time.Unix(0, t.Nanos).In(zone(t.Offset))
}

// String is the datetime with its offset.
func (t ZonedDateTime) String() string {
	return t.Time().Format("2006-01-02T15:04:05.999999999-07:00")
}

// A YearMonth is a duration in months, which is the half of the
// duration space that has no fixed length in nanoseconds: a month is
// 28, 29, 30 or 31 days depending on which one it lands on, so it
// cannot be a [time.Duration] without picking one.
type YearMonth struct {
	// Months is the length of the duration, negative for one that
	// runs backwards. A year is twelve of them.
	Months int64
}

// String is the duration in the spelling the language writes it in.
func (d YearMonth) String() string {
	months, sign := d.Months, ""
	if months < 0 {
		months, sign = -months, "-"
	}
	return fmt.Sprintf("%sP%dY%dM", sign, months/12, months%12)
}

// zone is a fixed zone at an offset in minutes east of UTC. UTC itself
// is the named zone rather than a nameless one at zero, so that a
// value written with no offset prints as a caller expects.
func zone(offset int32) *time.Location {
	if offset == 0 {
		return time.UTC
	}
	return time.FixedZone(offsetText(offset), int(offset)*60)
}

// offsetText is an offset in minutes as the standard spells it.
func offsetText(offset int32) string {
	if offset == 0 {
		return "Z"
	}
	sign := "+"
	if offset < 0 {
		sign, offset = "-", -offset
	}
	return fmt.Sprintf("%s%02d:%02d", sign, offset/60, offset%60)
}

// clock is a count of nanoseconds since midnight as a wall clock.
func clock(nanos int64) string {
	d := time.Duration(nanos)
	h := d / time.Hour
	m := d % time.Hour / time.Minute
	s := d % time.Minute / time.Second
	frac := d % time.Second
	if frac == 0 {
		return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	}
	return trimZeros(fmt.Sprintf("%02d:%02d:%02d.%09d", h, m, s, frac))
}

// trimZeros drops the trailing zeroes of a fractional second, which is
// what makes 12:00:00.500000000 print as 12:00:00.5.
func trimZeros(s string) string {
	for len(s) > 0 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	return s
}

// temporal builds the Go value for one kind, count and offset, which
// is what the ABI hands back for every temporal cell.
func temporal(kind TemporalKind, count int64, offset int32) (any, error) {
	switch kind {
	case KindDate:
		return Date{Days: int32(count)}, nil
	case KindLocalTime:
		return LocalTime{Nanos: count}, nil
	case KindZonedTime:
		return ZonedTime{Nanos: count, Offset: offset}, nil
	case KindLocalDateTime:
		return LocalDateTime{Nanos: count}, nil
	case KindZonedDateTime:
		return ZonedDateTime{Nanos: count, Offset: offset}, nil
	case KindYearMonthDuration:
		return YearMonth{Months: count}, nil
	case KindDayTimeDuration:
		return time.Duration(count), nil
	default:
		return nil, misuse(fmt.Sprintf("the engine gave back temporal kind %d, which this client does not know", kind))
	}
}
