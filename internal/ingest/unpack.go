package ingest

import (
	"archive/tar"
	"archive/zip"
	"compress/flate"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/google/uuid"

	"github.com/Njunge11/rejea/internal/documents"
	"github.com/Njunge11/rejea/internal/storage"
)

// encrypted is the bit in the flags of a zip entry that tells that the
// entry is encrypted.
const encrypted = 0x1

// contentFault is a fault in the content of an archive: a limit that it
// breaks, or data that the service cannot read. A new attempt cannot
// correct it.
type contentFault struct{ reason string }

func (f *contentFault) Error() string { return f.reason }

// isContentFault tells if an error is a contentFault.
func isContentFault(err error) bool {
	_, ok := errors.AsType[*contentFault](err)
	return ok
}

// permanent tells if a new attempt at the unpack cannot correct an error
// from the read of an archive: the content of the archive is the cause,
// and not the file storage, the network or the stop of the job.
func permanent(err error) bool {
	if storage.IsFault(err) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if isContentFault(err) {
		return true
	}
	if _, ok := errors.AsType[flate.CorruptInputError](err); ok {
		return true
	}
	for _, damaged := range []error{
		zip.ErrFormat, zip.ErrAlgorithm, zip.ErrChecksum,
		tar.ErrHeader, gzip.ErrHeader, gzip.ErrChecksum,
		io.ErrUnexpectedEOF,
	} {
		if errors.Is(err, damaged) {
			return true
		}
	}
	return false
}

// unpacker takes the PDF and Word files out of one archive and sends each
// one to the file storage as a file of its own. It uses no disk: it reads
// the archive from the file storage and sends each file as a stream.
//
// It never uses a name from the archive as a path. A name is text for the
// filename column only, and the service makes each storage key.
type unpacker struct {
	ctx   context.Context //nolint:containedctx // One unpacker does the work of one job.
	store *storage.Store
	cfg   Config
	// archive is the ID of the document of the archive.
	archive uuid.UUID

	bytes int64 // the bytes that came out of the archive
	files []documents.UnpackedFile
}

func (u *unpacker) tooManyFiles() error {
	return &contentFault{fmt.Sprintf("the archive has more than %d PDF and Word files", u.cfg.MaxArchiveFiles)}
}

func (u *unpacker) tooManyBytes() error {
	return &contentFault{fmt.Sprintf("the archive has more than %d bytes when unpacked", u.cfg.MaxUnpackedBytes)}
}

// unpack takes the files out of the archive of a document. With an error,
// it gives the files that it sent to the file storage before the error.
func (w *fileWorker) unpack(ctx context.Context, id uuid.UUID, key string) ([]documents.UnpackedFile, error) {
	u := &unpacker{
		ctx:     ctx,
		store:   w.store,
		cfg:     w.cfg,
		archive: id,
	}
	var err error
	switch documents.Ending(key) {
	case ".zip":
		err = u.zip(key)
	case ".tar":
		err = u.tar(key, false)
	default:
		err = u.tar(key, true)
	}
	if err != nil {
		// The caller removes the files that are in the file storage.
		return u.files, damaged("the service cannot read the archive", err)
	}
	if len(u.files) == 0 {
		return nil, &contentFault{"the archive has no PDF or Word file"}
	}
	return u.files, nil
}

// zip unpacks a zip. The list of the files is at the end of a zip, so the
// zip reader needs random access: it reads the archive in pieces.
func (u *unpacker) zip(key string) error {
	ranges, err := u.store.OpenRanges(u.ctx, key)
	if err != nil {
		return err
	}
	// The zip reader keeps the full list of the files in memory. A zip
	// that is only a very long list can use all the memory of the
	// service, so the list has a limit.
	list := &listGuard{source: ranges, left: u.cfg.MaxArchiveListBytes, limit: u.cfg.MaxArchiveListBytes}
	reader, err := zip.NewReader(list, ranges.Size())
	// A name that points out of the archive is not a risk here, because
	// no name is used as a path.
	if err != nil && !errors.Is(err, zip.ErrInsecurePath) {
		return damaged("the file is not a zip that the service can read", err)
	}
	list.complete = true
	// A zip declares the number and the sizes of its files. If these are
	// over a limit, the unpack stops before it sends a file. The sizes can
	// be false, so the real bytes are counted too.
	var wantedFiles []*zip.File
	var declared uint64
	for _, entry := range reader.File {
		if entry.Mode().IsRegular() && entry.UncompressedSize64 > 0 && wanted(cleanName(entry.Name)) {
			wantedFiles = append(wantedFiles, entry)
			declared += entry.UncompressedSize64
		}
	}
	if len(wantedFiles) > u.cfg.MaxArchiveFiles {
		return u.tooManyFiles()
	}
	if declared > uint64(u.cfg.MaxUnpackedBytes) { //nolint:gosec // The limit is 1 or more.
		return u.tooManyBytes()
	}
	for _, entry := range wantedFiles {
		name := cleanName(entry.Name)
		if entry.Flags&encrypted != 0 {
			return &contentFault{fmt.Sprintf("the file %q in the zip is encrypted", name)}
		}
		content, err := entry.Open()
		if err != nil {
			return damaged(fmt.Sprintf("the file %q in the zip is damaged", name), err)
		}
		// The zip reader compares the content with the checksum of the
		// entry when the content ends.
		err = u.add(name, u.metered(content))
		content.Close() //nolint:errcheck,gosec // The read of the content found each error.
		if err != nil {
			return damaged(fmt.Sprintf("the file %q in the zip is damaged", name), err)
		}
	}
	return nil
}

// tar unpacks a tar, with gzip compression or with none. The files of a
// tar come in sequence, so one stream from the file storage is sufficient.
func (u *unpacker) tar(key string, compressed bool) error {
	archive, err := u.store.Download(u.ctx, key)
	if err != nil {
		return err
	}
	defer archive.Close() //nolint:errcheck // The read of the archive found each error.

	var source io.Reader = archive
	if compressed {
		decompressed, err := gzip.NewReader(archive)
		if err != nil {
			return damaged("the file is not a gzip file that the service can read", err)
		}
		// Each byte that comes out of the gzip counts, also the bytes of
		// the files that the service does not keep.
		source = u.metered(decompressed)
	}
	reader := tar.NewReader(source)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		// A name that points out of the archive is not a risk here.
		if err != nil && !errors.Is(err, tar.ErrInsecurePath) {
			return damaged("the file is not a tar that the service can read", err)
		}
		name := cleanName(header.Name)
		if header.Typeflag != tar.TypeReg || header.Size == 0 || !wanted(name) {
			continue
		}
		var content io.Reader = reader
		if !compressed {
			content = u.metered(reader)
		}
		if err := u.add(name, content); err != nil {
			return damaged(fmt.Sprintf("the file %q in the tar is damaged", name), err)
		}
	}
}

// add sends one file from the archive to the file storage and computes its
// hash in the same pass.
func (u *unpacker) add(name string, content io.Reader) error {
	if len(u.files) == u.cfg.MaxArchiveFiles {
		return u.tooManyFiles()
	}
	key := documents.InnerKey(u.archive, len(u.files), name)
	sum := sha256.New()
	var size int64
	stream := &recorder{source: io.TeeReader(&meter{
		source: content,
		count:  &size,
		limit:  u.cfg.MaxFileBytes,
		fault:  fmt.Sprintf("the file %q in the archive has more than %d bytes", name, u.cfg.MaxFileBytes),
	}, sum)}
	if err := u.store.Upload(u.ctx, key, "", stream); err != nil {
		// The upload fails when its stream fails. The error of the stream
		// is then the cause, and it tells if a new attempt can help.
		if stream.err != nil {
			return stream.err
		}
		return err
	}
	u.files = append(u.files, documents.UnpackedFile{
		Filename:   name,
		StorageKey: key,
		Hash:       hex.EncodeToString(sum.Sum(nil)),
	})
	return nil
}

// metered counts the bytes that come out of the archive and stops at the
// limit. The count is of the real bytes: the sizes that an archive
// declares can be false.
func (u *unpacker) metered(source io.Reader) io.Reader {
	return &meter{
		source: source,
		count:  &u.bytes,
		limit:  u.cfg.MaxUnpackedBytes,
		fault:  u.tooManyBytes().Error(),
	}
}

// damaged makes a contentFault, with an explanation, from an error that
// the content of the archive caused. An error with a different cause stays
// as it is.
func damaged(explanation string, err error) error {
	if isContentFault(err) || !permanent(err) {
		return err
	}
	return &contentFault{explanation + ": " + err.Error()}
}

// cleanName makes the name of a file in an archive safe to keep as text.
// The names in a zip can be in an old encoding, and PostgreSQL refuses
// text that is not UTF-8.
func cleanName(name string) string {
	name = strings.ToValidUTF8(name, "�")
	name = strings.ReplaceAll(name, `\`, "/")
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	return strings.Trim(name, "/")
}

// wanted tells if the service keeps a file from an archive: a PDF or a
// Word file that is not a hidden file. macOS adds a hidden file with the
// same ending for each file, and these are not documents. An archive in an
// archive is not kept.
func wanted(name string) bool {
	if !documents.IsDocumentFile(name) {
		return false
	}
	for part := range strings.SplitSeq(name, "/") {
		if strings.HasPrefix(part, ".") || part == "__MACOSX" {
			return false
		}
	}
	return true
}

// meter counts the bytes of a stream and fails when the count goes over a
// limit.
type meter struct {
	source io.Reader
	count  *int64
	limit  int64
	fault  string
}

func (m *meter) Read(p []byte) (int, error) {
	n, err := m.source.Read(p)
	*m.count += int64(n)
	if *m.count > m.limit {
		return n, &contentFault{m.fault}
	}
	return n, err
}

// recorder keeps the first error of a stream.
type recorder struct {
	source io.Reader
	err    error
}

func (r *recorder) Read(p []byte) (int, error) {
	n, err := r.source.Read(p)
	if err != nil && !errors.Is(err, io.EOF) && r.err == nil {
		r.err = err
	}
	return n, err
}

// listGuard limits the bytes that the zip reader reads for the list of the
// files. When the list is complete, it lets each read through.
type listGuard struct {
	source   io.ReaderAt
	left     int64
	limit    int64
	complete bool
}

func (g *listGuard) ReadAt(p []byte, offset int64) (int, error) {
	if !g.complete {
		g.left -= int64(len(p))
		if g.left < 0 {
			return 0, &contentFault{fmt.Sprintf("the list of the files in the zip has more than %d bytes", g.limit)}
		}
	}
	return g.source.ReadAt(p, offset)
}
