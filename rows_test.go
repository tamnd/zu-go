package zu

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAResultKnowsItsColumnsAndItsSize(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, "UNWIND [1, 2, 3] AS n RETURN n, n * 2 AS twice")

	want := []string{"n", "twice"}
	got := rows.Columns()
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("the columns are %v and the statement named %v", got, want)
	}
	if rows.Len() != 3 {
		t.Errorf("three rows were unwound and the result holds %d", rows.Len())
	}
	if s := rows.GQLStatus(); s != "00000" {
		t.Errorf("a statement that answered with columns completed with %q", s)
	}
	if n := rows.Notices(); len(n) != 0 {
		t.Errorf("a statement that raised nothing carries %d notices", len(n))
	}
}

func TestAStatementWithNothingToGiveBackSaysSo(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, "START TRANSACTION")
	defer conn.Exec(t.Context(), "ROLLBACK")

	if rows.Len() != 0 {
		t.Errorf("a statement with no result answered %d rows", rows.Len())
	}
	if s := rows.GQLStatus(); s != "00001" {
		t.Errorf("successful completion with the result omitted is 00001, and this is %q", s)
	}
}

func TestEveryScalarScansIntoTheTypeItIs(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, `RETURN 7 AS i, 2.5 AS f, "hi" AS s, true AS b`)
	if !rows.Next() {
		t.Fatal("a statement that answered one row has none")
	}

	var i int64
	var f float64
	var s string
	var b bool
	if err := rows.Scan(&i, &f, &s, &b); err != nil {
		t.Fatalf("scanning the four scalars: %v", err)
	}
	if i != 7 || f != 2.5 || s != "hi" || !b {
		t.Errorf("the row read back as %d %v %q %v", i, f, s, b)
	}
}

func TestAnIntegerScansIntoEverySizeThatHoldsIt(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, "RETURN 7 AS n")
	if !rows.Next() {
		t.Fatal("no row")
	}

	var (
		i    int
		i8   int8
		i16  int16
		i32  int32
		i64  int64
		u    uint
		u32  uint32
		u64  uint64
		wide float64
	)
	for _, dest := range []any{&i, &i8, &i16, &i32, &i64, &u, &u32, &u64, &wide} {
		if err := rows.Scan(dest); err != nil {
			t.Errorf("scanning 7 into %T: %v", dest, err)
		}
	}
	if i != 7 || i8 != 7 || i16 != 7 || i32 != 7 || i64 != 7 || u != 7 || u32 != 7 || u64 != 7 || wide != 7 {
		t.Errorf("7 read back as %d %d %d %d %d %d %d %d %v", i, i8, i16, i32, i64, u, u32, u64, wide)
	}
}

func TestAnIntegerThatDoesNotFitIsRefusedRatherThanTruncated(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, "RETURN 300 AS n")
	if !rows.Next() {
		t.Fatal("no row")
	}

	var small int8
	err := rows.Scan(&small)
	if err == nil {
		t.Fatalf("300 scanned into an int8 as %d", small)
	}
	if !errors.Is(err, Misuse) {
		t.Errorf("the failure is %v", err)
	}
	if small != 0 {
		t.Errorf("the refused scan wrote %d anyway", small)
	}
}

func TestANamedTypeOverAnIntegerScans(t *testing.T) {
	type userID int64
	conn := memory(t)
	rows := query(t, conn, "RETURN 7 AS n")
	if !rows.Next() {
		t.Fatal("no row")
	}

	var id userID
	if err := rows.Scan(&id); err != nil {
		t.Fatalf("scanning into a named type: %v", err)
	}
	if id != 7 {
		t.Errorf("the id read back as %d", id)
	}
}

func TestACellReadAsSomethingItIsNotNamesTheColumn(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, `RETURN "hi" AS greeting`)
	if !rows.Next() {
		t.Fatal("no row")
	}

	var n int64
	err := rows.Scan(&n)
	if err == nil {
		t.Fatal("a string scanned into an int64")
	}
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("the failure is not one of ours: %#v", err)
	}
	for _, want := range []string{"greeting", "string", "int64"} {
		if !strings.Contains(e.Message, want) {
			t.Errorf("the message %q does not say %q", e.Message, want)
		}
	}
}

func TestANullNeedsSomewhereThatHoldsOne(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, "RETURN null AS nothing")
	if !rows.Next() {
		t.Fatal("no row")
	}

	var n int64
	if err := rows.Scan(&n); err == nil {
		t.Error("a null scanned into an int64, which would have read as a zero")
	}

	var p *int64
	if err := rows.Scan(&p); err != nil {
		t.Errorf("a null does not scan into a pointer: %v", err)
	}
	if p != nil {
		t.Errorf("a null read back as %v", *p)
	}

	var a any
	if err := rows.Scan(&a); err != nil {
		t.Errorf("a null does not scan into an any: %v", err)
	}
	if a != nil {
		t.Errorf("a null read back as %v", a)
	}
}

func TestAValueScansIntoAPointerAndTheValueIsThere(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, "RETURN 7 AS n")
	if !rows.Next() {
		t.Fatal("no row")
	}

	var p *int64
	if err := rows.Scan(&p); err != nil {
		t.Fatalf("scanning into a pointer: %v", err)
	}
	if p == nil || *p != 7 {
		t.Errorf("7 read back as %v", p)
	}
}

func TestTooManyDestinationsIsRefused(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, "RETURN 1 AS one")
	if !rows.Next() {
		t.Fatal("no row")
	}

	var a, b int64
	if err := rows.Scan(&a, &b); err == nil {
		t.Error("two destinations for one column worked")
	}
}

func TestScanBeforeNextIsRefusedRatherThanReadingRowZero(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, "RETURN 1 AS one")

	var n int64
	if err := rows.Scan(&n); err == nil {
		t.Error("scanning without calling Next read a row anyway")
	}
	if rows.Err() == nil {
		t.Error("the failure was not recorded for the loop that did not check")
	}
}

func TestAListAndARecordComeBackWhole(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, `RETURN [1, 2, 3] AS l, {a: 1, b: "x"} AS r`)
	if !rows.Next() {
		t.Fatal("no row")
	}

	var list []any
	var rec Record
	if err := rows.Scan(&list, &rec); err != nil {
		t.Fatalf("scanning a list and a record: %v", err)
	}
	if len(list) != 3 || list[0] != int64(1) || list[2] != int64(3) {
		t.Errorf("the list read back as %v", list)
	}
	if len(rec) != 2 || rec["a"] != int64(1) || rec["b"] != "x" {
		t.Errorf("the record read back as %v", rec)
	}
}

func TestEverySevenTemporalsReadAsThemselves(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, `RETURN
		DATE "2024-01-15" AS d,
		LOCAL TIME "10:30:00" AS lt,
		ZONED TIME "10:30:00+02:00" AS zt,
		LOCAL DATETIME "2024-01-15T10:30:00" AS ldt,
		ZONED DATETIME "2024-01-15T10:30:00+02:00" AS zdt,
		DURATION "P1Y2M" AS ym,
		DURATION "PT1H30M" AS dt`)
	if !rows.Next() {
		t.Fatal("no row")
	}

	var (
		d   Date
		lt  LocalTime
		zt  ZonedTime
		ldt LocalDateTime
		zdt ZonedDateTime
		ym  YearMonth
		dur time.Duration
	)
	if err := rows.Scan(&d, &lt, &zt, &ldt, &zdt, &ym, &dur); err != nil {
		t.Fatalf("scanning the seven temporals: %v", err)
	}

	if got := d.String(); got != "2024-01-15" {
		t.Errorf("the date read back as %q", got)
	}
	if got := lt.Duration(); got != 10*time.Hour+30*time.Minute {
		t.Errorf("the local time read back as %v", got)
	}
	if zt.Offset != 120 {
		t.Errorf("the zoned time is at offset %d minutes", zt.Offset)
	}
	if got := ldt.Time().Format(time.RFC3339); got != "2024-01-15T10:30:00Z" {
		t.Errorf("the local datetime read back as %q", got)
	}
	if got := zdt.Time().Format(time.RFC3339); got != "2024-01-15T10:30:00+02:00" {
		t.Errorf("the zoned datetime read back as %q", got)
	}
	if ym.Months != 14 {
		t.Errorf("a year and two months is %d months", ym.Months)
	}
	if dur != 90*time.Minute {
		t.Errorf("the day-time duration read back as %v", dur)
	}
}

func TestTheThreeTemporalsThatNameAnInstantScanIntoATime(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, `RETURN
		DATE "2024-01-15" AS d,
		LOCAL DATETIME "2024-01-15T10:30:00" AS ldt,
		ZONED DATETIME "2024-01-15T10:30:00+02:00" AS zdt`)
	if !rows.Next() {
		t.Fatal("no row")
	}

	var d, ldt, zdt time.Time
	if err := rows.Scan(&d, &ldt, &zdt); err != nil {
		t.Fatalf("scanning three temporals into time.Time: %v", err)
	}
	if got := d.Format(time.RFC3339); got != "2024-01-15T00:00:00Z" {
		t.Errorf("a date at midnight is %q", got)
	}
	if got := zdt.Format(time.RFC3339); got != "2024-01-15T10:30:00+02:00" {
		t.Errorf("the zoned datetime is %q", got)
	}
	_ = ldt
}

func TestATimeOfDayIsNotAnInstant(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, `RETURN LOCAL TIME "10:30:00" AS lt`)
	if !rows.Next() {
		t.Fatal("no row")
	}

	var when time.Time
	if err := rows.Scan(&when); err == nil {
		t.Errorf("a time of day became the instant %v, which is a date somebody invented", when)
	}
}

func TestAWholeColumnComesBackWithoutACopy(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, "UNWIND [1, 2, 3, 4] AS n RETURN n, n * 1.5 AS f")

	ints, err := rows.Int64s(0)
	if err != nil {
		t.Fatalf("reading a whole column of integers: %v", err)
	}
	if len(ints) != 4 || ints[0] != 1 || ints[3] != 4 {
		t.Errorf("the column read back as %v", ints)
	}

	floats, err := rows.Float64s(1)
	if err != nil {
		t.Fatalf("reading a whole column of floats: %v", err)
	}
	if len(floats) != 4 || floats[3] != 6 {
		t.Errorf("the column read back as %v", floats)
	}

	valid, err := rows.Valid(0)
	if err != nil {
		t.Fatalf("reading which rows of a column hold a value: %v", err)
	}
	for i, ok := range valid {
		if ok == 0 {
			t.Errorf("row %d of a column with no nulls in it reads as null", i)
		}
	}
}

func TestAColumnReadAsSomethingItIsNotIsRefused(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, `UNWIND ["a", "b"] AS s RETURN s`)

	if _, err := rows.Int64s(0); !errors.Is(err, Misuse) {
		t.Errorf("a column of strings read as integers answers %v", err)
	}
	if _, err := rows.NodeOffsets(0); !errors.Is(err, Misuse) {
		t.Errorf("a column of strings read as node offsets answers %v", err)
	}
	if _, err := rows.Int64s(7); !errors.Is(err, Misuse) {
		t.Errorf("column 7 of a result with one column answers %v", err)
	}
}

func TestAColumnOfAClosedResultIsRefusedRatherThanRead(t *testing.T) {
	conn := memory(t)
	rows, err := conn.Query(t.Context(), "UNWIND [1, 2] AS n RETURN n")
	if err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := rows.Int64s(0); !errors.Is(err, Misuse) {
		t.Errorf("reading a column of a closed result answers %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Errorf("closing a result twice: %v", err)
	}
}

func TestARowAnswersTheTypeOfEachCell(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, `RETURN 1 AS i, "s" AS s, null AS n, [1] AS l`)
	if !rows.Next() {
		t.Fatal("no row")
	}

	want := []Type{TypeInt, TypeString, TypeNull, TypeList}
	for c, w := range want {
		got, err := rows.Row().Type(c)
		if err != nil {
			t.Fatalf("asking the type of column %d: %v", c, err)
		}
		if got != w {
			t.Errorf("column %d holds a %v and the statement wrote a %v", c, got, w)
		}
	}
}

func TestTheRangeLoopWalksEveryRowOnce(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, "UNWIND [1, 2, 3] AS n RETURN n")

	var seen []int64
	for row := range rows.All() {
		var n int64
		if err := row.Scan(&n); err != nil {
			t.Fatalf("scanning row %d: %v", len(seen), err)
		}
		seen = append(seen, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("the loop ended with %v", err)
	}
	if len(seen) != 3 || seen[0] != 1 || seen[2] != 3 {
		t.Errorf("the loop saw %v", seen)
	}
}

func TestBreakingOutOfTheRangeLoopStopsIt(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, "UNWIND [1, 2, 3] AS n RETURN n")

	seen := 0
	for range rows.All() {
		seen++
		break
	}
	if seen != 1 {
		t.Errorf("a loop that broke after one row saw %d", seen)
	}
	if err := rows.Err(); err != nil {
		t.Errorf("breaking out of the loop reported %v", err)
	}
}

func TestARowIsReachableByItsIndexWithoutMovingTheCursor(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, "UNWIND [10, 20, 30] AS n RETURN n")

	// Out of order, twice each, and before Next has been called at all.
	// The result is an array and not a cursor, so none of that is a
	// trick being played on it.
	for _, tc := range []struct {
		at   int64
		want int64
	}{{2, 30}, {0, 10}, {2, 30}, {1, 20}} {
		var got int64
		if err := rows.RowAt(tc.at).Scan(&got); err != nil {
			t.Fatalf("reading row %d: %v", tc.at, err)
		}
		if got != tc.want {
			t.Errorf("row %d holds %d and the statement wrote %d", tc.at, got, tc.want)
		}
	}

	if got, err := rows.RowAt(1).Type(0); err != nil || got != TypeInt {
		t.Errorf("the type of row 1 is %v, %v", got, err)
	}
	if got, err := rows.RowAt(0).Value(0); err != nil || got != int64(10) {
		t.Errorf("the value of row 0 is %#v, %v", got, err)
	}

	// None of that moved where the loop is, so the loop still sees all
	// three from the start.
	seen := 0
	for range rows.All() {
		seen++
	}
	if seen != 3 {
		t.Errorf("the loop saw %d rows after the result was read by index", seen)
	}
	if err := rows.Err(); err != nil {
		t.Errorf("reading by index and then looping reported %v", err)
	}
}

func TestARowIndexThatIsNotARowIsRefused(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, "UNWIND [1, 2, 3] AS n RETURN n")

	for _, at := range []int64{-1, 3, 1 << 40} {
		var got int64
		err := rows.RowAt(at).Scan(&got)
		if !errors.Is(err, Misuse) {
			t.Errorf("row %d is refused with %v", at, err)
		}
		if _, err := rows.RowAt(at).Type(0); !errors.Is(err, Misuse) {
			t.Errorf("the type of row %d is refused with %v", at, err)
		}
		if _, err := rows.RowAt(at).Value(0); !errors.Is(err, Misuse) {
			t.Errorf("the value of row %d is refused with %v", at, err)
		}
	}
	if err := rows.Err(); !errors.Is(err, Misuse) {
		t.Errorf("the result did not record the refusal: %v", err)
	}
}
