package zu

/*
#include <zu.h>
*/
import "C"

import (
	"errors"
	"strconv"
	"strings"
)

// A Status is what a call answered, which is the coarse half of a
// failure: whether anything was done, whether the connection is still
// good, and whether the caller or the engine is the one that has
// something to fix. The condition a user reads is [Error.Code], not
// this.
//
// A Status is itself an error, so the statuses double as the sentinels
// [errors.Is] compares against.
//
//	if errors.Is(err, zu.Conflict) {
//		// nothing of the write was applied, so try it again
//	}
type Status int32

// The statuses, with the same numbers as the C ABI. New ones are
// appended there and never inserted, so a number that is missing here
// is a number that is held for something not written yet.
const (
	// OK means the call did what it was asked.
	OK Status = C.ZU_OK
	// Done means the call was well formed and there is nothing to
	// read, such as a column of a result with no rows.
	Done Status = C.ZU_DONE
	// Refused means the engine declined the work and said why. This
	// is the status a statement that will not parse, a table that is
	// not there and a value of the wrong type all arrive with, and
	// the GQLSTATUS condition on the error is what tells them apart.
	Refused Status = C.ZU_ERROR
	// Misuse means the contract was broken: a handle that is closed,
	// an index out of range, or a column read as something it does
	// not hold. Nothing was done and nothing is wrong with the
	// database.
	Misuse Status = C.ZU_MISUSE
	// Concurrent means two goroutines used one connection at once.
	// Nothing was done. Connect again rather than share.
	Concurrent Status = C.ZU_MISUSE_CONCURRENT
	// Closed means a statement was used after its connection closed.
	// Nothing was done, and the statement is still safe to close.
	Closed Status = C.ZU_MISUSE_CLOSED
	// Interrupted means the statement was stopped while it ran.
	// Nothing is wrong with the connection and the next statement on
	// it runs normally.
	Interrupted Status = C.ZU_INTERRUPTED
	// Conflict means a write lost to a concurrent one. None of it was
	// applied, which is why this is the one status worth a retry loop.
	Conflict Status = C.ZU_CONFLICT
	// Corrupt means the file says something that cannot be true.
	Corrupt Status = C.ZU_CORRUPT
	// Unsupported means this build does not implement the call, as
	// against declining to run it.
	Unsupported Status = C.ZU_UNSUPPORTED
	// IO means the operating system refused a read or a write.
	IO Status = C.ZU_IO
)

// Error makes a Status usable as a sentinel. The text is the status
// and nothing else, because a status on its own is all that is known
// when there is no error handle to read.
func (s Status) Error() string {
	return "zu: " + s.String()
}

// String is the status in words.
func (s Status) String() string {
	switch s {
	case OK:
		return "ok"
	case Done:
		return "nothing to read"
	case Refused:
		return "refused"
	case Misuse:
		return "misuse"
	case Concurrent:
		return "the connection is in use by another goroutine"
	case Closed:
		return "the connection is closed"
	case Interrupted:
		return "interrupted"
	case Conflict:
		return "write conflict"
	case Corrupt:
		return "corrupt database"
	case Unsupported:
		return "unsupported"
	case IO:
		return "io error"
	default:
		return "status " + strconv.Itoa(int(s))
	}
}

// A Severity says whether a diagnostic record is a failure at all. An
// exception replaces a result and arrives as an error from the call
// that raised it; a warning rides along with a result and is read from
// [Rows.Notices].
type Severity int32

// The severities, from the standard's own set.
const (
	SeveritySuccess       Severity = C.ZU_SEVERITY_SUCCESS
	SeverityNoData        Severity = C.ZU_SEVERITY_NO_DATA
	SeverityWarning       Severity = C.ZU_SEVERITY_WARNING
	SeverityInformational Severity = C.ZU_SEVERITY_INFORMATIONAL
	SeverityException     Severity = C.ZU_SEVERITY_EXCEPTION
)

// String is the severity in the standard's word for it.
func (s Severity) String() string {
	switch s {
	case SeveritySuccess:
		return "success"
	case SeverityNoData:
		return "no data"
	case SeverityWarning:
		return "warning"
	case SeverityInformational:
		return "informational"
	case SeverityException:
		return "exception"
	default:
		return "severity " + strconv.Itoa(int(s))
	}
}

// A Position is where in a statement a condition was raised. Line and
// Column are 1-based and Column counts characters, so a line of
// multi-byte text does not read as wider than it looks. Offset is the
// same place as a 0-based byte index into the statement, for a caller
// that slices the text rather than printing it, and it is always on a
// character boundary.
//
// Not every failure has one. A division by zero happens while the
// statement runs and has no token to point at, and an io error has no
// statement at all, so a caller that underlines the token asks
// [Position.Valid] first.
type Position struct {
	// Line is which line of the statement, counted from one. Zero
	// means there is no position, which is what [Position.Valid]
	// reports.
	Line uint32
	// Column is which character of that line, counted from one, so a
	// line of multi-byte text does not read as wider than it looks.
	Column uint32
	// Offset is the same place as a byte index into the whole
	// statement, counted from zero and always on a character
	// boundary.
	Offset uint32
}

// Valid reports whether there is a position at all. Lines are counted
// from one, so the zero Position is the absence of one.
func (p Position) Valid() bool {
	return p.Line > 0
}

// String is the position as a caller would write it in a compiler
// message, or the empty string when there is none.
func (p Position) String() string {
	if !p.Valid() {
		return ""
	}
	return strconv.FormatUint(uint64(p.Line), 10) + ":" + strconv.FormatUint(uint64(p.Column), 10)
}

// An Error is one diagnostic record from the engine, with the fields
// the record has rather than one sentence to parse back out. The code
// picks what a program does next, the severity decides whether it is a
// failure at all, and neither survives being formatted into prose.
//
//	var e *zu.Error
//	if errors.As(err, &e) && e.Retryable {
//		// run it again
//	}
//
// A string the failure does not carry is empty rather than absent,
// with one exception worth knowing: Code, StandardText and DocURL are
// all empty together for an engine-internal failure, which has no
// condition rather than one that would be a guess.
type Error struct {
	// Status is what the call that raised this answered.
	Status Status
	// Code is the GQLSTATUS condition, five characters, empty when
	// the failure has none.
	Code string
	// Message is zu's own account, naming the table, the token or the
	// value. Printing it alone is a complete report.
	Message string
	// StandardText is the standard's words for the condition class
	// and subclass, which is what a conformance harness grades.
	StandardText string
	// DocURL is where the condition is written up, so that a program
	// can hand a reader a page rather than five characters to search
	// for.
	DocURL string
	// Severity is whether this is an exception, a warning or a note.
	Severity Severity
	// Retryable is whether running the same statement again could
	// work. A write that lost to a concurrent one is the yes, because
	// nothing of it was applied. Text that will not parse is the no,
	// and so is a statement the caller interrupted, which did not
	// fail so much as stop.
	Retryable bool
	// Position is where in the statement, when the condition has a
	// place. Ask [Position.Valid] before reading it.
	Position Position
	// Excerpt is the line that position is on, without its newline,
	// which Column counts characters into. It is empty when there is
	// no position and when the line is longer than anyone would read
	// under a caret, since a line cut to fit would put the column
	// somewhere it is not.
	Excerpt string

	// cause is the context error behind an interruption, so that a
	// query stopped by a cancelled context answers to
	// errors.Is(err, context.Canceled) as well as to the status.
	cause error
}

// Error is the message the engine wrote, prefixed so that a log line
// says where it came from. The condition goes in front of it when the
// message does not already start with it, which the engine's own
// messages do, because a reader who recognises the condition should
// not have to read the sentence to find it and should not have to read
// it twice either.
func (e *Error) Error() string {
	switch {
	case e.Message == "":
		return e.Status.Error()
	case e.Code != "" && !strings.HasPrefix(e.Message, e.Code):
		return "zu: " + e.Code + ": " + e.Message
	default:
		return "zu: " + e.Message
	}
}

// Is matches an Error against a [Status] sentinel, which is what makes
// errors.Is(err, zu.Conflict) the way to ask the coarse question
// without unwrapping to the record.
func (e *Error) Is(target error) bool {
	s, ok := target.(Status)
	return ok && s == e.Status
}

// Unwrap gives up the context error behind an interruption. A query
// stopped because its context was cancelled answers to both
// errors.Is(err, context.Canceled) and errors.Is(err, zu.Interrupted),
// which are the two ways a caller could reasonably ask.
func (e *Error) Unwrap() error {
	return e.cause
}

// take reads an error handle into a Go value and frees it. The handle
// belongs to the caller of the C function that wrote it, and this is
// the only place it is released, so every path that can produce one
// goes through here.
//
// A nil handle still makes an Error, because a call that failed
// without saying why still failed, and a caller matching on a status
// should not have to tell the two shapes apart.
func take(status C.zu_status, handle *C.zu_error) *Error {
	if handle == nil {
		return &Error{Status: Status(status), Message: Status(status).String()}
	}
	defer C.zu_error_free(handle)

	var n C.size_t
	e := &Error{Status: Status(C.zu_error_status(handle))}
	e.Message = text(C.zu_error_message(handle, &n), n)
	e.Code = text(C.zu_error_code(handle, &n), n)
	e.StandardText = text(C.zu_error_standard_text(handle, &n), n)
	e.DocURL = text(C.zu_error_doc_url(handle, &n), n)
	e.Excerpt = text(C.zu_error_excerpt(handle, &n), n)
	e.Severity = Severity(C.zu_error_severity(handle))
	e.Retryable = C.zu_error_retryable(handle) == 1

	var line, column, offset C.uint32_t
	if C.zu_error_position(handle, &line, &column) == C.ZU_OK {
		C.zu_error_offset(handle, &offset)
		e.Position = Position{Line: uint32(line), Column: uint32(column), Offset: uint32(offset)}
	}

	// The status the handle carries is the status of the call that
	// made it, and the two disagree only for a notice, which is read
	// off a result rather than returned. Trusting the handle would
	// lose the status for a call that failed with a record that has
	// none, so the returned one wins when the handle has nothing.
	if e.Status == OK && Status(status) != OK {
		e.Status = Status(status)
	}
	return e
}

// fail turns a status and an error handle into an error, or into nil
// when the call worked. Every fallible call in this package ends with
// it, which is what keeps the handle from being freed twice or not at
// all.
func fail(status C.zu_status, handle *C.zu_error) error {
	if status == C.ZU_OK {
		if handle != nil {
			C.zu_error_free(handle)
		}
		return nil
	}
	return take(status, handle)
}

// misuse is the error for a call this binding refused before it
// reached the engine, which is a contract broken on the Go side and
// carries no condition because no statement ran.
func misuse(what string) error {
	return &Error{Status: Misuse, Message: what}
}

// ErrDone is what a transaction that is already finished answers to a
// second Commit or Rollback. The usual `defer tx.Rollback()` beside a
// commit is the reason it exists: that path is not a failure, and it
// should not have to be spelled as one.
var ErrDone = errors.New("zu: the transaction is already finished")
