// Command testset works with the question file of the test set.
//
//	testset check <question file>
//
// check loads the file and examines it: each question has its kind, its
// answer and its source pages, and the set has the size that the plan
// gives. It prints the number of documents and of questions of each kind.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Njunge11/rejea/internal/testset"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "testset:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	if len(args) != 2 || args[0] != "check" {
		return fmt.Errorf("usage: testset check <question file>   (the file of this repository is testset/questions.json)")
	}
	set, err := testset.Load(args[1])
	if err != nil {
		return err
	}
	types := map[string]bool{}
	for _, doc := range set.Documents {
		types[doc.Type] = true
	}
	count := set.Count()
	var report strings.Builder
	fmt.Fprintf(&report, "The question file %s:\n", args[1])
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
