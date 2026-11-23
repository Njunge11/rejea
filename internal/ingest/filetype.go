package ingest

import "strings"

// The endings of the file names that the service can work on. A longer
// ending comes before a shorter ending that it ends with.
var (
	documentEndings = []string{".pdf", ".docx", ".doc"}
	archiveEndings  = []string{".zip", ".tar.gz", ".tgz", ".tar"}
)

// FileTypes is the list of the endings for a message to a person.
const FileTypes = ".pdf, .docx, .doc, .zip, .tar, .tar.gz or .tgz"

// Ending gives the ending of a file name in lower case when the service can
// work on that type of file. If not, it gives "".
func Ending(filename string) string {
	if ending := endingFrom(filename, documentEndings); ending != "" {
		return ending
	}
	return endingFrom(filename, archiveEndings)
}

func endingFrom(filename string, endings []string) string {
	name := strings.ToLower(filename)
	for _, ending := range endings {
		if strings.HasSuffix(name, ending) {
			return ending
		}
	}
	return ""
}
