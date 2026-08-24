// The temporal half of the encoding.
//
// These parsers exist so that the corpus reader is a second opinion
// about the text rather than a second call into the same library the
// client calls, and a second opinion is only worth having if it says no
// to the same things. So most of this file is texts that look like
// temporal values and are not: a basic-form date, a leap second, a
// fraction of ten digits, an offset past the standard's own limit.
//
// The other thing tested here is which of the two duration types a text
// comes to. A month is not a number of days, the client has a type for
// each, and which one a case means is part of what the case asserts.

package corpus

import (
	"testing"
	"time"

	zu "github.com/tamnd/zu-go"
)

func TestADateIsTheExtendedFormAndTheCalendarAnswersForFebruary(t *testing.T) {
	for _, c := range []struct {
		text string
		days int32
	}{
		{"1970-01-01", 0},
		{"1970-01-02", 1},
		{"1969-12-31", -1},
		{"2024-02-29", 19782},
		{"0001-01-01", -719162},
		{"9999-12-31", 2932896},
	} {
		got, ok := parseDate(c.text)
		if !ok {
			t.Errorf("%s is not a date, and it is one", Quote(c.text))
			continue
		}
		if got != (zu.Date{Days: c.days}) {
			t.Errorf("%s came to %s, and it is %d days from the epoch",
				Quote(c.text), Show(got), c.days)
		}
		// And back out again, which is what a failure report prints.
		if back := showDate(zu.Date{Days: c.days}); back != c.text {
			t.Errorf("%d days prints as %s, and it was written %s",
				c.days, Quote(back), Quote(c.text))
		}
	}
	for _, text := range []string{
		"20240101",   // the basic form, which the other runners would not read
		"2024-1-1",   // fields that are not padded
		"2023-02-30", // a day February does not have
		"2023-13-01", // a month the year does not have
		"2023-00-01", // and the two that are zero
		"2023-01-00",
		"2024-02-29x", // something after the date
		"+2024-01-01", // a sign, which the field reader does not take
		"2024/01/01",  // the wrong separator
		"",
	} {
		if got, ok := parseDate(text); ok {
			t.Errorf("%s read as %s, and it is not a date", Quote(text), Show(got))
		}
	}
}

func TestATimeIsSecondsAlwaysAndAFractionWhenThereIsOne(t *testing.T) {
	for _, c := range []struct {
		text  string
		nanos int64
	}{
		{"00:00:00", 0},
		{"23:59:59", 86399 * nanosPerSecond},
		{"12:34:56.789000000", 45296*nanosPerSecond + 789000000},
		{"12:34:56.789", 45296*nanosPerSecond + 789000000},
		{"12:34:56.1", 45296*nanosPerSecond + 100000000},
		{"23:59:59.999999999", 86400*nanosPerSecond - 1},
	} {
		got, ok := parseLocalTime(c.text)
		if !ok {
			t.Errorf("%s is not a time, and it is one", Quote(c.text))
			continue
		}
		if got != (zu.LocalTime{Nanos: c.nanos}) {
			t.Errorf("%s came to %s, and it is %d nanoseconds", Quote(c.text), Show(got), c.nanos)
		}
	}
	// Printed with nine digits when there is a fraction and none when
	// there is not, which is what the reference runner writes and not
	// what the client's own String gives.
	for _, c := range []struct {
		nanos int64
		want  string
	}{
		{0, "00:00:00"},
		{100000000, "00:00:00.100000000"},
		{1, "00:00:00.000000001"},
		{86400*nanosPerSecond - 1, "23:59:59.999999999"},
	} {
		if got := showClock(c.nanos); got != c.want {
			t.Errorf("%d nanoseconds prints as %s, and it should be %s",
				c.nanos, Quote(got), Quote(c.want))
		}
	}
	for _, text := range []string{
		"123456",              // the basic form
		"1:02:03",             // fields that are not padded
		"24:00:00",            // the hour a day does not have
		"23:60:00",            // and the minute
		"23:59:60",            // the leap second, which the count does not have
		"12:34:56.",           // a point with nothing after it
		"12:34:56.1234567890", // ten digits, finer than the engine counts
		"12:34:56.-1",         // a sign inside the fraction
		"12:34",               // no seconds
		"12:34:56Z",           // an offset, which a local time does not carry
		"",
	} {
		if got, ok := parseLocalTime(text); ok {
			t.Errorf("%s read as %s, and it is not a time", Quote(text), Show(got))
		}
	}
}

// A zoned time carries the clock as written rather than moved to UTC,
// which is what makes 12:00:00+07:00 and 05:00:00Z two values here and
// not one.
func TestAZonedTimeKeepsTheClockItWasWrittenWith(t *testing.T) {
	got, ok := parseZonedTime("12:00:00+07:00")
	if !ok {
		t.Fatalf("12:00:00+07:00 is not a zoned time")
	}
	want := zu.ZonedTime{Nanos: 12 * nanosPerHour, Offset: 7 * 60}
	if got != want {
		t.Errorf("came to %s, and the clock is kept as written", Show(got))
	}
	utc, _ := parseZonedTime("05:00:00Z")
	if utc == got {
		t.Errorf("05:00:00Z and 12:00:00+07:00 came to one value, and they are two")
	}
	if got := Show(want); got != `ZONEDTIME "12:00:00+07:00"` {
		t.Errorf("prints as %s", Quote(got))
	}
}

// A zoned datetime is held as the instant, so two texts an hour apart
// in zones an hour apart are one value and hold one count.
func TestAZonedDateTimeIsHeldAsTheInstantAndTheOffsetBesideIt(t *testing.T) {
	east, ok := parseZonedDateTime("2024-01-01T07:00:00+07:00")
	if !ok {
		t.Fatalf("2024-01-01T07:00:00+07:00 is not a zoned datetime")
	}
	utc, ok := parseZonedDateTime("2024-01-01T00:00:00Z")
	if !ok {
		t.Fatalf("2024-01-01T00:00:00Z is not a zoned datetime")
	}
	if east.(zu.ZonedDateTime).Nanos != utc.(zu.ZonedDateTime).Nanos {
		t.Errorf("%s and %s are the same instant and hold different counts", Show(east), Show(utc))
	}
	if east == utc {
		t.Errorf("the offset is not kept, and it is part of what a case asserts")
	}
	// Printed back into the zone it was written in, which is the wall
	// clock a case reads.
	if got := Show(east); got != `ZONEDDATETIME "2024-01-01T07:00:00+07:00"` {
		t.Errorf("prints as %s", Quote(got))
	}
	// A zero offset prints as Z, whichever of the two spellings went in.
	plain, _ := parseZonedDateTime("2024-01-01T00:00:00+00:00")
	if got := Show(plain); got != `ZONEDDATETIME "2024-01-01T00:00:00Z"` {
		t.Errorf("a zero offset prints as %s", Quote(got))
	}
}

func TestALocalDateTimeCountsFromTheEpochAndReadsBackBeforeIt(t *testing.T) {
	for _, c := range []struct {
		text  string
		nanos int64
	}{
		{"1970-01-01T00:00:00", 0},
		{"1970-01-02T00:00:00", nanosPerDay},
		{"1969-12-31T23:59:59", -nanosPerSecond},
		{"2024-01-15T10:00:00", 19737*nanosPerDay + 10*nanosPerHour},
	} {
		got, ok := parseLocalDateTime(c.text)
		if !ok {
			t.Errorf("%s is not a datetime, and it is one", Quote(c.text))
			continue
		}
		if got != (zu.LocalDateTime{Nanos: c.nanos}) {
			t.Errorf("%s came to %s", Quote(c.text), Show(got))
		}
		// The date of an instant before the epoch is the day it is on and
		// not the day after it, which is what the floored division is for.
		if back := showStamp(c.nanos); back != c.text {
			t.Errorf("%d nanoseconds prints as %s, and it was written %s",
				c.nanos, Quote(back), Quote(c.text))
		}
	}
	for _, text := range []string{
		"2024-01-15 10:00:00",  // a space where the T goes
		"2024-01-15",           // no time
		"10:00:00",             // no date
		"2024-01-15T10:00:00Z", // an offset, which a local datetime does not carry
		"",
	} {
		if got, ok := parseLocalDateTime(text); ok {
			t.Errorf("%s read as %s, and it is not a datetime", Quote(text), Show(got))
		}
	}
}

func TestAnOffsetIsZOrTheExtendedFormWithinTheStandardsLimit(t *testing.T) {
	for _, c := range []struct {
		text    string
		rest    string
		minutes int32
	}{
		{"12:00:00Z", "12:00:00", 0},
		{"12:00:00+00:00", "12:00:00", 0},
		{"12:00:00+07:00", "12:00:00", 420},
		{"12:00:00-05:30", "12:00:00", -330},
		{"12:00:00+18:00", "12:00:00", 1080},
		{"12:00:00-18:00", "12:00:00", -1080},
	} {
		rest, minutes, ok := splitOffset(c.text)
		if !ok {
			t.Errorf("%s has no offset, and it has one", Quote(c.text))
			continue
		}
		if rest != c.rest || minutes != c.minutes {
			t.Errorf("%s split into %s and %d, and it should be %s and %d",
				Quote(c.text), Quote(rest), minutes, Quote(c.rest), c.minutes)
		}
	}
	for _, text := range []string{
		"12:00:00+18:01", // past the standard's own limit
		"12:00:00+19:00",
		"12:00:00+0700",  // the basic form
		"12:00:00+07",    // hours alone
		"12:00:00+07:60", // a minute an hour does not have
		"12:00:00",       // no offset at all
		"12:00:00 07:00", // no sign
		"+07:00",         // an offset and nothing before it, which is too short to split
	} {
		if rest, minutes, ok := splitOffset(text); ok && rest != "" {
			t.Errorf("%s split into %s and %d, and it carries no offset this reader takes",
				Quote(text), Quote(rest), minutes)
		}
	}
	for _, c := range []struct {
		minutes int32
		want    string
	}{
		{0, "Z"},
		{420, "+07:00"},
		{-330, "-05:30"},
		{1080, "+18:00"},
	} {
		if got := showOffset(c.minutes); got != c.want {
			t.Errorf("%d prints as %s, and it should be %s", c.minutes, Quote(got), Quote(c.want))
		}
	}
}

// A duration is months or it is nanoseconds and never both, because
// adding a month to a date is a different operation from adding thirty
// days and a type holding both would have to say which happens first.
func TestADurationIsOneOfTheTwoKindsTheEngineKeepsApart(t *testing.T) {
	for _, c := range []struct {
		text string
		want any
	}{
		{"P1Y", zu.YearMonth{Months: 12}},
		{"P1Y2M", zu.YearMonth{Months: 14}},
		{"P2M", zu.YearMonth{Months: 2}},
		{"-P1Y2M", zu.YearMonth{Months: -14}},
		// The one text the fields decide and the numbers cannot: no
		// months and no nanoseconds, told apart by what was written.
		{"P0M", zu.YearMonth{Months: 0}},
		{"P1D", 24 * time.Hour},
		{"P1W", 7 * 24 * time.Hour},
		{"PT1H", time.Hour},
		{"PT1M", time.Minute},
		{"PT1S", time.Second},
		{"PT0S", time.Duration(0)},
		{"PT0.250000000S", 250 * time.Millisecond},
		{"PT0.5S", 500 * time.Millisecond},
		{"P1DT2H3M4S", 24*time.Hour + 2*time.Hour + 3*time.Minute + 4*time.Second},
		{"-PT1H", -time.Hour},
		{"+PT1H", time.Hour},
	} {
		got, ok := parseDuration(c.text)
		if !ok {
			t.Errorf("%s is not a duration, and it is one", Quote(c.text))
			continue
		}
		if got != c.want {
			t.Errorf("%s came to %s, and it should be %s", Quote(c.text), Show(got), Show(c.want))
		}
	}
	for _, text := range []string{
		"P1Y1D",  // a field of each kind, refused rather than guessed at
		"P1M1S",  // likewise, through the time part
		"P",      // a P with nothing under it
		"PT",     // a T with nothing after it
		"P1DT",   // and a T on the end of a date part
		"1Y",     // no P
		"P1X",    // a unit nothing here knows
		"P1",     // digits with no unit after them
		"PT1",    // likewise in the time part
		"P0.5Y",  // a fraction of a year, whose length depends on which one
		"P0.5M",  // and of a month
		"P0.5D",  // and of a day, which a leap second makes not quite exact
		"PT0.5H", // a fraction anywhere but on the seconds
		"PT0.5M",
		"PT1.S",              // a point with nothing after it
		"PT0.1234567890S",    // ten digits
		"P1Y2M3W4DT5H6M7.8S", // every field at once, which is both kinds
		"",
	} {
		if got, ok := parseDuration(text); ok {
			t.Errorf("%s read as %s, and it is not a duration this reader takes",
				Quote(text), Show(got))
		}
	}
}

// A duration prints as the text that parses back to it, which is what
// lets a failure report be pasted into a case.
func TestADurationPrintsAsTheTextThatReadsBackToIt(t *testing.T) {
	for _, c := range []struct {
		value any
		want  string
	}{
		{zu.YearMonth{Months: 0}, "P0M"},
		{zu.YearMonth{Months: 1}, "P1M"},
		{zu.YearMonth{Months: 12}, "P1Y"},
		{zu.YearMonth{Months: 14}, "P1Y2M"},
		{zu.YearMonth{Months: -14}, "-P1Y2M"},
		{time.Duration(0), "PT0S"},
		{time.Second, "PT1S"},
		{time.Minute, "PT1M"},
		{time.Hour, "PT1H"},
		{24 * time.Hour, "P1D"},
		{25 * time.Hour, "P1DT1H"},
		{250 * time.Millisecond, "PT0.250000000S"},
		{time.Second + time.Nanosecond, "PT1.000000001S"},
		{-time.Hour, "-PT1H"},
		{24*time.Hour + 2*time.Hour + 3*time.Minute + 4*time.Second, "P1DT2H3M4S"},
		{time.Hour + time.Minute, "PT1H1M"},
	} {
		var got string
		switch v := c.value.(type) {
		case zu.YearMonth:
			got = showMonths(v.Months)
		case time.Duration:
			got = showNanos(int64(v))
		}
		if got != c.want {
			t.Errorf("%s prints as %s, and it should be %s", Show(c.value), Quote(got), Quote(c.want))
		}
		// And back, which is the half that says the spelling is the one
		// this reader takes and not merely one that looks right.
		back, ok := parseDuration(c.want)
		if !ok || back != c.value {
			t.Errorf("%s read back as %v, %v", Quote(c.want), back, ok)
		}
	}
}

// number is the field reader every parser above goes through, and it is
// not ParseInt: a sign or an underscore inside a temporal field is a
// text some other runner would refuse.
func TestATemporalFieldIsDigitsAndNothingElse(t *testing.T) {
	for _, c := range []struct {
		text string
		want int64
	}{
		{"0", 0},
		{"07", 7},
		{"2024", 2024},
	} {
		got, ok := number(c.text)
		if !ok || got != c.want {
			t.Errorf("number(%s) is %d, %v", Quote(c.text), got, ok)
		}
	}
	for _, text := range []string{"", "+1", "-1", "1_0", " 1", "1 ", "0x1", "١٢"} {
		if got, ok := number(text); ok {
			t.Errorf("number(%s) is %d, and it is not a field", Quote(text), got)
		}
	}
}
