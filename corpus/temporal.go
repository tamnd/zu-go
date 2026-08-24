package corpus

// The temporal half of the encoding, written out rather than handed to
// time.Parse.
//
// A corpus reader is a second opinion about the text, and a second
// opinion that calls the same library the client calls is not one.
// time.Parse also takes a great deal this encoding does not: a layout
// with a fractional second matches text with none, a numeric zone
// matches "Z" under some layouts and not others, and none of that is
// visible at the call site. Writing the four spellings out is thirty
// lines and it says exactly what is accepted.
//
// The spellings are the ones the engine prints, which is the extended
// ISO 8601 form and nothing else: 2024-01-01 for a date, 12:34:56 with
// an optional fraction of one to nine digits for a time, the two joined
// with a T for a datetime, and Z or +07:00 for an offset. A basic-form
// 20240101 is refused, because a case that writes one is a case the
// other runners would read differently or not at all.
//
// Go holds all of it exactly. A date is a count of days and everything
// else is a count of nanoseconds, which is the resolution the engine
// keeps, so unlike the Python runner this one has no notion of a value
// written too finely to hold.

import (
	"strconv"
	"strings"
	"time"

	zu "github.com/tamnd/zu-go"
)

const (
	nanosPerSecond = int64(time.Second)
	nanosPerMinute = int64(time.Minute)
	nanosPerHour   = int64(time.Hour)
	nanosPerDay    = 24 * nanosPerHour
	secondsPerDay  = 86400
)

// parseDate is a date, as the count of days from 1970-01-01 that the
// client holds one as.
func parseDate(text string) (any, bool) {
	days, ok := dateDays(text)
	if !ok {
		return nil, false
	}
	return zu.Date{Days: days}, true
}

// parseLocalTime is a time of day with no offset on the end.
func parseLocalTime(text string) (any, bool) {
	nanos, ok := clockNanos(text)
	if !ok {
		return nil, false
	}
	return zu.LocalTime{Nanos: nanos}, true
}

// parseZonedTime is a time of day with an offset, which it carries as
// written: the count of nanoseconds is midnight in the offset's own
// day rather than midnight UTC, which is what the client's ZonedTime
// holds and what makes 12:00:00+07:00 and 05:00:00Z two values rather
// than one.
func parseZonedTime(text string) (any, bool) {
	rest, offset, ok := splitOffset(text)
	if !ok {
		return nil, false
	}
	nanos, ok := clockNanos(rest)
	if !ok {
		return nil, false
	}
	return zu.ZonedTime{Nanos: nanos, Offset: offset}, true
}

// parseLocalDateTime is a date and a time with no offset, as the count
// of nanoseconds from 1970-01-01T00:00:00 read with no zone at all.
func parseLocalDateTime(text string) (any, bool) {
	nanos, ok := stampNanos(text)
	if !ok {
		return nil, false
	}
	return zu.LocalDateTime{Nanos: nanos}, true
}

// parseZonedDateTime is an instant and the offset it was written with.
//
// The client holds the instant in UTC and the offset beside it, so the
// wall clock that was written is moved back by the offset to get there.
// Two texts an hour apart in zones an hour apart are the same instant
// and hold the same count, which is the point of keeping it that way.
func parseZonedDateTime(text string) (any, bool) {
	rest, offset, ok := splitOffset(text)
	if !ok {
		return nil, false
	}
	nanos, ok := stampNanos(rest)
	if !ok {
		return nil, false
	}
	return zu.ZonedDateTime{Nanos: nanos - int64(offset)*nanosPerMinute, Offset: offset}, true
}

// dateDays is YYYY-MM-DD as a count of days from the epoch. The date is
// built and read back rather than checked field by field, because that
// is the calendar answering the question about February rather than
// this file having an opinion about it.
func dateDays(text string) (int32, bool) {
	if len(text) != 10 || text[4] != '-' || text[7] != '-' {
		return 0, false
	}
	year, ok := number(text[0:4])
	if !ok {
		return 0, false
	}
	month, ok := number(text[5:7])
	if !ok {
		return 0, false
	}
	day, ok := number(text[8:10])
	if !ok {
		return 0, false
	}
	when := time.Date(int(year), time.Month(month), int(day), 0, 0, 0, 0, time.UTC)
	// A date the calendar does not have comes back as the one it rolled
	// over into, so 2023-02-30 reads back as March and is refused here.
	y, m, d := when.Date()
	if int64(y) != year || int64(m) != month || int64(d) != day {
		return 0, false
	}
	return int32(when.Unix() / secondsPerDay), true
}

// clockNanos is HH:MM:SS, with a fraction of one to nine digits when
// there is one, as nanoseconds since midnight.
func clockNanos(text string) (int64, bool) {
	head, frac, dotted := strings.Cut(text, ".")
	if len(head) != 8 || head[2] != ':' || head[5] != ':' {
		return 0, false
	}
	hours, ok := number(head[0:2])
	if !ok {
		return 0, false
	}
	minutes, ok := number(head[3:5])
	if !ok {
		return 0, false
	}
	seconds, ok := number(head[6:8])
	if !ok {
		return 0, false
	}
	// No leap second, because the engine has no value for one: a time
	// is nanoseconds since midnight and 23:59:60 is a second the count
	// does not have.
	if hours > 23 || minutes > 59 || seconds > 59 {
		return 0, false
	}
	nanos := hours*nanosPerHour + minutes*nanosPerMinute + seconds*nanosPerSecond
	if !dotted {
		return nanos, true
	}
	// A point with nothing after it is not a fraction, and ten digits is
	// finer than the engine counts, so neither is read as the number it
	// resembles.
	if frac == "" || len(frac) > 9 {
		return 0, false
	}
	part, ok := number(frac)
	if !ok {
		return 0, false
	}
	for i := len(frac); i < 9; i++ {
		part *= 10
	}
	return nanos + part, true
}

// stampNanos is a date and a time joined with a T, as nanoseconds from
// 1970-01-01T00:00:00.
func stampNanos(text string) (int64, bool) {
	day, clock, joined := strings.Cut(text, "T")
	if !joined {
		return 0, false
	}
	days, ok := dateDays(day)
	if !ok {
		return 0, false
	}
	nanos, ok := clockNanos(clock)
	if !ok {
		return 0, false
	}
	return int64(days)*nanosPerDay + nanos, true
}

// splitOffset takes the offset off the end of a zoned value and gives
// back what came before it, in minutes east of UTC.
//
// Zero is written Z rather than +00:00, which is what the engine prints,
// and both are read here because a case may assert either. Which one it
// was is not kept, since it is not part of the value: the engine holds
// an offset in minutes and prints zero as Z whichever way it went in.
func splitOffset(text string) (rest string, offset int32, ok bool) {
	if after, found := strings.CutSuffix(text, "Z"); found {
		return after, 0, true
	}
	if len(text) < 7 {
		return "", 0, false
	}
	mark := text[len(text)-6]
	if mark != '+' && mark != '-' {
		return "", 0, false
	}
	zone := text[len(text)-6:]
	if zone[3] != ':' {
		return "", 0, false
	}
	hours, good := number(zone[1:3])
	if !good {
		return "", 0, false
	}
	minutes, good := number(zone[4:6])
	if !good || minutes > 59 {
		return "", 0, false
	}
	total := hours*60 + minutes
	// The standard's own limit, which is wider than any zone in use and
	// is here so that a typo lands as a refusal rather than as a date a
	// day away from the one that was meant.
	if total > 18*60 {
		return "", 0, false
	}
	if mark == '-' {
		total = -total
	}
	return text[:len(text)-6], int32(total), true
}

// number is a run of ASCII digits as the number it spells. Not
// [strconv.ParseInt], which takes a sign and an underscore between
// digits, neither of which belongs inside a temporal field.
func number(text string) (int64, bool) {
	if text == "" {
		return 0, false
	}
	var out int64
	for i := 0; i < len(text); i++ {
		if text[i] < '0' || text[i] > '9' {
			return 0, false
		}
		out = out*10 + int64(text[i]-'0')
	}
	return out, true
}

// parseDuration is an ISO 8601 duration, in the two kinds the engine
// keeps apart.
//
// A duration is months or it is nanoseconds and never both, because a
// month is not a number of days: adding one to a date is a different
// operation from adding thirty of them, and a type that held both would
// have to say which happens first. The client has a type for each, so
// which one this is is part of what the case asserts.
//
// The text says which. A duration whose fields are years and months is
// the month kind and everything else is the nanosecond kind, and a
// duration with a field of each is refused rather than guessed at. That
// leaves one text the fields decide and the numbers cannot, which is a
// duration of nothing: P0M is no months and PT0S is no nanoseconds, and
// they are two values here where the Python runner has to call them one.
func parseDuration(text string) (any, bool) {
	negative := strings.HasPrefix(text, "-")
	if negative || strings.HasPrefix(text, "+") {
		text = text[1:]
	}
	body, found := strings.CutPrefix(text, "P")
	if !found {
		return nil, false
	}
	day, clock, dated := strings.Cut(body, "T")
	// A P with nothing under it is not a duration, and neither is a T
	// with nothing after it.
	if day == "" && clock == "" {
		return nil, false
	}
	if dated && clock == "" {
		return nil, false
	}

	var months, nanos int64
	sawMonths := !dated
	for _, field := range pieces(day) {
		if !field.ok {
			return nil, false
		}
		switch field.unit {
		case 'Y':
			months += field.whole * 12
		case 'M':
			months += field.whole
		case 'W':
			nanos += field.whole * 7 * nanosPerDay
			sawMonths = false
		case 'D':
			nanos += field.whole * nanosPerDay
			sawMonths = false
		default:
			return nil, false
		}
		// A fraction of a year, a month, a week or a day is a length
		// that depends on which one it lands on, so it is refused here
		// rather than turned into a number of nanoseconds that is right
		// for some of them.
		if field.frac != 0 {
			return nil, false
		}
	}
	for _, field := range pieces(clock) {
		if !field.ok {
			return nil, false
		}
		switch field.unit {
		case 'H':
			nanos += field.whole * nanosPerHour
		case 'M':
			nanos += field.whole * nanosPerMinute
		case 'S':
			nanos += field.whole*nanosPerSecond + field.frac
		default:
			return nil, false
		}
		if field.frac != 0 && field.unit != 'S' {
			return nil, false
		}
	}
	if months != 0 && nanos != 0 {
		return nil, false
	}
	if negative {
		months, nanos = -months, -nanos
	}
	if months != 0 || (nanos == 0 && sawMonths) {
		return zu.YearMonth{Months: months}, true
	}
	return time.Duration(nanos), true
}

// A piece is one number and the letter after it, which is what a
// duration is a run of.
type piece struct {
	whole int64
	// frac is the fraction of a second, in nanoseconds, for the one
	// field that is allowed one.
	frac int64
	unit byte
	ok   bool
}

// pieces splits half a duration into its fields. A half that does not
// split gives back one field that is not ok, so that the caller refuses
// the text at the same place it refuses a unit it does not know.
func pieces(text string) []piece {
	var out []piece
	start := 0
	for i := 0; i < len(text); i++ {
		c := text[i]
		if (c >= '0' && c <= '9') || c == '.' {
			continue
		}
		one := piece{unit: c, ok: true}
		head, frac, dotted := strings.Cut(text[start:i], ".")
		var good bool
		if one.whole, good = number(head); !good {
			one.ok = false
		}
		if dotted {
			if frac == "" || len(frac) > 9 {
				one.ok = false
			} else if one.frac, good = number(frac); !good {
				one.ok = false
			} else {
				for n := len(frac); n < 9; n++ {
					one.frac *= 10
				}
			}
		}
		out = append(out, one)
		start = i + 1
	}
	// Digits with no unit after them, which is the one thing left over
	// that a caller has to hear about.
	if start != len(text) {
		out = append(out, piece{ok: false})
	}
	return out
}

// showDate is a date the way the engine prints one.
func showDate(d zu.Date) string {
	year, month, day := d.Time().Date()
	return pad(int64(year), 4) + "-" + pad(int64(month), 2) + "-" + pad(int64(day), 2)
}

// showClock is a count of nanoseconds since midnight the way the engine
// prints one, which is seconds always and a fraction of nine digits
// when there is one.
//
// Nine and not the shortest that reads back, which is what the client's
// own String gives: the report this goes into is diffed against the one
// the reference runner writes, and that one writes nine.
func showClock(nanos int64) string {
	hours := nanos / nanosPerHour
	minutes := nanos % nanosPerHour / nanosPerMinute
	seconds := nanos % nanosPerMinute / nanosPerSecond
	frac := nanos % nanosPerSecond
	out := pad(hours, 2) + ":" + pad(minutes, 2) + ":" + pad(seconds, 2)
	if frac != 0 {
		out += "." + pad(frac, 9)
	}
	return out
}

// showStamp is a count of nanoseconds from the epoch as a date and a
// time joined with a T.
func showStamp(nanos int64) string {
	days := floorDiv(nanos, nanosPerDay)
	return showDate(zu.Date{Days: int32(days)}) + "T" + showClock(nanos-days*nanosPerDay)
}

// showOffset is an offset in minutes east of UTC, which is Z at zero
// rather than +00:00.
func showOffset(offset int32) string {
	if offset == 0 {
		return "Z"
	}
	sign := "+"
	if offset < 0 {
		sign, offset = "-", -offset
	}
	return sign + pad(int64(offset)/60, 2) + ":" + pad(int64(offset)%60, 2)
}

// showMonths is a month duration as the text that parses back to it: a
// field that is zero is left out, and a duration with nothing left in
// it is P0M, because P on its own is not a value.
func showMonths(count int64) string {
	sign := ""
	if count < 0 {
		sign, count = "-", -count
	}
	years, months := count/12, count%12
	out := sign + "P"
	if years != 0 {
		out += strconv.FormatInt(years, 10) + "Y"
	}
	if months != 0 || years == 0 {
		out += strconv.FormatInt(months, 10) + "M"
	}
	return out
}

// showNanos is a nanosecond duration as the text that parses back to
// it, under the same rule, with PT0S for the one that is empty.
func showNanos(nanos int64) string {
	sign := ""
	if nanos < 0 {
		sign, nanos = "-", -nanos
	}
	days, rest := nanos/nanosPerDay, nanos%nanosPerDay
	out := sign + "P"
	if days != 0 {
		out += strconv.FormatInt(days, 10) + "D"
	}
	if rest == 0 && days != 0 {
		return out
	}
	out += "T"
	hours := rest / nanosPerHour
	minutes := rest % nanosPerHour / nanosPerMinute
	seconds := rest % nanosPerMinute / nanosPerSecond
	frac := rest % nanosPerSecond
	if hours != 0 {
		out += strconv.FormatInt(hours, 10) + "H"
	}
	if minutes != 0 {
		out += strconv.FormatInt(minutes, 10) + "M"
	}
	if seconds != 0 || frac != 0 || (hours == 0 && minutes == 0) {
		out += strconv.FormatInt(seconds, 10)
		if frac != 0 {
			out += "." + pad(frac, 9)
		}
		out += "S"
	}
	return out
}

// pad is a number in at least width digits, zeroes in front of it.
func pad(n int64, width int) string {
	text := strconv.FormatInt(n, 10)
	for len(text) < width {
		text = "0" + text
	}
	return text
}

// floorDiv rounds towards minus infinity rather than towards zero,
// which is what turns an instant before the epoch into the day it is on
// rather than the day after it.
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}
