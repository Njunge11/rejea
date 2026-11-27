package documents

import "strings"

// The endings of the file names that the service can work on. A longer
// ending comes before a shorter ending that it ends with.
var (
	documentEndings = []string{".pdf", ".docx", ".doc", ".markdown", ".md"}
	markdownEndings = []string{".markdown", ".md"}
	archiveEndings  = []string{".zip", ".tar.gz", ".tgz", ".tar"}
)

// FileTypes is the list of the endings for a message to a person.
const FileTypes = ".pdf, .docx, .doc, .md, .markdown, .zip, .tar, .tar.gz or .tgz"

// Ending gives the ending of a file name in lower case when the service can
// work on that type of file. If not, it gives "".
func Ending(filename string) string {
	if ending := endingFrom(filename, documentEndings); ending != "" {
		return ending
	}
	return endingFrom(filename, archiveEndings)
}

// IsArchive tells if a file name has the ending of an archive: a file that
// holds other files.
func IsArchive(filename string) bool { return endingFrom(filename, archiveEndings) != "" }

// IsDocumentFile tells if a file name has the ending of a PDF, a Word file
// or a Markdown file.
func IsDocumentFile(filename string) bool { return endingFrom(filename, documentEndings) != "" }

// IsPDF tells if a file name has the ending of a PDF.
func IsPDF(filename string) bool { return endingFrom(filename, []string{".pdf"}) != "" }

// IsMarkdown tells if a file name has the ending of a Markdown file.
func IsMarkdown(filename string) bool { return endingFrom(filename, markdownEndings) != "" }

func endingFrom(filename string, endings []string) string {
	name := strings.ToLower(filename)
	for _, ending := range endings {
		if strings.HasSuffix(name, ending) {
			return ending
		}
	}
	return ""
}
