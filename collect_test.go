package zu

import (
	"errors"
	"strings"
	"testing"
	"time"
)

type pair struct {
	N    int64  `zu:"n"`
	Word string `zu:"word"`
}

func TestCollectFillsAStructFromTheColumnsThatMatch(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, `UNWIND [1, 2, 3] AS n RETURN n, "w" AS word`)

	got, err := Collect[pair](rows)
	if err != nil {
		t.Fatalf("collecting: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("three rows came back as %d", len(got))
	}
	if got[2].N != 3 || got[2].Word != "w" {
		t.Errorf("the last row is %+v", got[2])
	}
}

func TestAFieldWithNoTagMatchesItsColumnWithoutRegardToCase(t *testing.T) {
	type row struct {
		Name string
		Age  int64
	}
	conn := memory(t)
	rows := query(t, conn, `RETURN "ada" AS name, 36 AS AGE`)

	got, err := Collect[row](rows)
	if err != nil {
		t.Fatalf("collecting: %v", err)
	}
	if got[0].Name != "ada" || got[0].Age != 36 {
		t.Errorf("the row is %+v", got[0])
	}
}

func TestAColumnNoFieldClaimsIsSkippedRatherThanRefused(t *testing.T) {
	type row struct {
		Name string `zu:"name"`
	}
	conn := memory(t)
	rows := query(t, conn, `RETURN "ada" AS name, 36 AS age`)

	got, err := Collect[row](rows)
	if err != nil {
		t.Fatalf("collecting a result with a column the struct does not have: %v", err)
	}
	if got[0].Name != "ada" {
		t.Errorf("the row is %+v", got[0])
	}
}

func TestAFieldTaggedAsSkippedIsNotFilledEvenByItsOwnName(t *testing.T) {
	type row struct {
		Name string `zu:"name"`
		Age  int64  `zu:"-"`
	}
	conn := memory(t)
	rows := query(t, conn, `RETURN "ada" AS name, 36 AS age`)

	got, err := Collect[row](rows)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Age != 0 {
		t.Errorf("a field tagged out of the mapping was filled with %d", got[0].Age)
	}
}

func TestAStructThatMatchesNoColumnIsRefusedRatherThanReadEmpty(t *testing.T) {
	type row struct {
		Missing string `zu:"missing"`
	}
	conn := memory(t)
	rows := query(t, conn, `RETURN "ada" AS name`)

	_, err := Collect[row](rows)
	if !errors.Is(err, Misuse) {
		t.Fatalf("collecting into a struct that matches nothing answers %v", err)
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("the refusal does not say what the columns were: %v", err)
	}
}

func TestCollectReadsOneColumnIntoASingleValue(t *testing.T) {
	conn := memory(t)

	ints, err := Collect[int64](query(t, conn, "UNWIND [1, 2, 3] AS n RETURN n"))
	if err != nil {
		t.Fatalf("collecting integers: %v", err)
	}
	if len(ints) != 3 || ints[1] != 2 {
		t.Errorf("the integers are %v", ints)
	}

	words, err := Collect[string](query(t, conn, `UNWIND ["a", "b"] AS w RETURN w`))
	if err != nil {
		t.Fatalf("collecting strings: %v", err)
	}
	if len(words) != 2 || words[0] != "a" {
		t.Errorf("the strings are %v", words)
	}
}

func TestASingleValueNeedsAResultOfOneColumn(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, `RETURN 1 AS n, 2 AS m`)

	_, err := Collect[int64](rows)
	if !errors.Is(err, Misuse) {
		t.Fatalf("collecting two columns into one value answers %v", err)
	}
	if !strings.Contains(err.Error(), "more than one column") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

func TestATypeThatIsAStructAndStillOneValueIsReadAsOne(t *testing.T) {
	conn := memory(t)

	// A Date has a field and is not a row of columns, and neither is a
	// time.Time, whose fields are not even exported.
	dates, err := Collect[Date](query(t, conn, `RETURN DATE "2024-01-15" AS d`))
	if err != nil {
		t.Fatalf("collecting dates: %v", err)
	}
	if dates[0].String() != "2024-01-15" {
		t.Errorf("the date is %v", dates[0])
	}

	when, err := Collect[time.Time](query(t, conn, `RETURN DATETIME "2024-01-15T10:30:00Z" AS d`))
	if err != nil {
		t.Fatalf("collecting instants: %v", err)
	}
	if when[0].UTC().Hour() != 10 {
		t.Errorf("the instant is %v", when[0])
	}
}

func TestIterStopsAtTheFirstFailureAndSaysWhichColumn(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, `UNWIND [1, 2, 3] AS n RETURN n`)

	var seen int
	var failure error
	for _, err := range Iter[string](rows) {
		if err != nil {
			failure = err
			break
		}
		seen++
	}
	if seen != 0 {
		t.Errorf("%d rows read before the failure", seen)
	}
	if failure == nil {
		t.Fatal("reading integers as strings worked")
	}
	if !strings.Contains(failure.Error(), "n") {
		t.Errorf("the failure does not name the column: %v", failure)
	}
}

func TestIterStopsWhenTheCallerDoes(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, "UNWIND [1, 2, 3, 4, 5] AS n RETURN n")

	var seen []int64
	for n, err := range Iter[int64](rows) {
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, n)
		if n == 2 {
			break
		}
	}
	if len(seen) != 2 {
		t.Errorf("the loop read %v after breaking at the second row", seen)
	}
	// The rest of the result is still there, because breaking out of
	// the loop does not close anything.
	if rows.Len() != 5 {
		t.Errorf("the result says it has %d rows", rows.Len())
	}
}

func TestCollectOnAResultAlreadyPartlyReadTakesWhatIsLeft(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, "UNWIND [1, 2, 3, 4] AS n RETURN n")

	if !rows.Next() {
		t.Fatal("no first row")
	}
	rest, err := Collect[int64](rows)
	if err != nil {
		t.Fatalf("collecting the rest: %v", err)
	}
	if len(rest) != 3 || rest[0] != 2 {
		t.Errorf("what was left is %v", rest)
	}
}

func TestCollectAResultWithNoRowsGivesAnEmptySliceRatherThanNil(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, "UNWIND [] AS n RETURN n")

	got, err := Collect[int64](rows)
	if err != nil {
		t.Fatalf("collecting nothing: %v", err)
	}
	if got == nil {
		t.Error("a result with no rows collected to nil rather than to an empty slice")
	}
	if len(got) != 0 {
		t.Errorf("a result with no rows collected to %v", got)
	}
}
