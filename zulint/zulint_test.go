package zulint_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/tamnd/zu-go/zulint"
)

// The client the testdata imports is a stub with the real import path
// and the real method names, so none of this needs cgo, a library or a
// database. An analyzer reads source, and these read the source that
// people actually write.

func TestViewAfterClose(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), zulint.ViewAfterClose, "viewafterclose")
}

func TestRowsErr(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), zulint.RowsErr, "rowserr")
}

func TestConnShare(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), zulint.ConnShare, "connshare")
}

// Every check is in the list a driver runs, so adding one and
// forgetting to name it is a failure here rather than a check nobody
// ever ran.
func TestEveryCheckIsInTheList(t *testing.T) {
	want := map[string]bool{"viewafterclose": true, "rowserr": true, "connshare": true}
	got := map[string]bool{}
	for _, a := range zulint.Analyzers() {
		got[a.Name] = true
		if a.Doc == "" {
			t.Errorf("%s has no Doc, and a driver prints it in -help", a.Name)
		}
		if a.URL == "" {
			t.Errorf("%s has no URL, and a reader wants somewhere to go", a.Name)
		}
	}
	for name := range want {
		if !got[name] {
			t.Errorf("Analyzers does not include %s", name)
		}
	}
	for name := range got {
		if !want[name] {
			t.Errorf("Analyzers includes %s, which this test does not know about", name)
		}
	}
}
