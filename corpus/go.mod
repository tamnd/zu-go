// A module of its own beside the client, for the reason zuarrow is
// one: nobody importing zu-go to query a graph should carry a YAML
// reader, a value encoding and a test runner along with it. This is
// the thing CI runs and the thing a contributor runs by hand, and it
// is not part of the client's surface.
//
// It depends on the client and on nothing else. The Arrow half reads
// the C Data Interface directly rather than through arrow-go, so a
// checkout that runs the corpus pulls no third party module at all.
module github.com/tamnd/zu-go/corpus

go 1.26.6

require github.com/tamnd/zu-go v0.0.0
