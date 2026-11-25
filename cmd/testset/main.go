// Command testset works with the question file of the test set and with
// the search reports.
//
//	testset check <question file>
//	testset report <report file>
//	testset compare <saved report file> <report file>
//
// check loads the question file and examines it: each question has its
// kind, its answer and its source pages, and the set has the size that the
// plan gives. It prints the number of documents and of questions of each
// kind.
//
// report prints a search report (make search-report makes one). compare
// prints a report and its differences from a saved report.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Njunge11/rejea/internal/testset"
)

const usage = `usage:
  testset check <question file>                   examine the question file (testset/questions.json)
  testset report <report file>                    print a search report
  testset compare <saved report file> <report file>
                                                  print a search report and its differences from a saved report`

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "testset:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	switch {
	case len(args) == 2 && args[0] == "check":
		return check(args[1], stdout)
	case len(args) == 2 && args[0] == "report":
		report, err := testset.ReadReport(args[1])
		if err != nil {
			return err
		}
		_, err = io.WriteString(stdout, report.Text())
		return err
	case len(args) == 3 && args[0] == "compare":
		saved, err := testset.ReadReport(args[1])
		if err != nil {
			return err
		}
		latest, err := testset.ReadReport(args[2])
		if err != nil {
			return err
		}
		_, err = io.WriteString(stdout, latest.Text()+"\n"+testset.Compare(saved, latest))
		return err
	}
	return errors.New(usage)
}

// check examines a question file and prints its counts.
func check(path string, stdout io.Writer) error {
	set, err := testset.Load(path)
	if err != nil {
		return err
	}
	types := map[string]bool{}
	for _, doc := range set.Documents {
		types[doc.Type] = true
	}
	count := set.Count()
	var report strings.Builder
	fmt.Fprintf(&report, "The question file %s:\n", path)
	fmt.Fprintf(&report, "  documents: %d, of %d types\n", len(set.Documents), len(types))
	fmt.Fprintf(&report, "  questions: %d\n", len(set.Questions))
	for _, kind := range testset.Kinds {
		fmt.Fprintf(&report, "    %-10s %d\n", kind, count[kind])
	}
	size := set.Size()
	if size == nil {
		report.WriteString("The question file is correct.\n")
	}
	if _, err := io.WriteString(stdout, report.String()); err != nil {
		return err
	}
	return size
}
