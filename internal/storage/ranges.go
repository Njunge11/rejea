package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// keptPieces is the number of pieces that a RangeReader keeps in memory. A
// zip reader moves between the list of files at the end of the zip and the
// file that it unpacks, so it needs two.
const keptPieces = 2

// RangeReader gives random access to one file in the bucket, with no copy
// of the file on a disk. It reads the file in pieces of a fixed size, one
// request for each piece, and keeps the last pieces in memory. A reader
// that asks for a few bytes at a time, such as a zip reader, thus sends
// one request for each piece and not one for each read.
//
// All its requests are for the same version of the file: S3 refuses a
// request when the file changed after the RangeReader was opened.
type RangeReader struct {
	ctx    context.Context //nolint:containedctx // io.ReaderAt has no context argument.
	store  *Store
	key    string
	etag   *string
	size   int64
	mu     sync.Mutex
	pieces map[int64][]byte
	order  []int64
}

// OpenRanges makes a RangeReader for the file with the key. The context
// applies to each read.
func (s *Store) OpenRanges(ctx context.Context, key string) (*RangeReader, error) {
	head, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, &Fault{fmt.Errorf("look for %q: %w", key, err)}
	}
	return &RangeReader{
		ctx:    ctx,
		store:  s,
		key:    key,
		etag:   head.ETag,
		size:   aws.ToInt64(head.ContentLength),
		pieces: map[int64][]byte{},
	}, nil
}

// Size gives the size of the file in bytes.
func (r *RangeReader) Size() int64 { return r.size }

// ReadAt reads len(p) bytes of the file from the offset. It is io.ReaderAt.
func (r *RangeReader) ReadAt(p []byte, offset int64) (int, error) {
	if offset < 0 {
		return 0, errors.New("storage: a read before the start of the file")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	done := 0
	for done < len(p) && offset < r.size {
		index := offset / r.store.rangeBytes
		piece, err := r.piece(index)
		if err != nil {
			return done, err
		}
		n := copy(p[done:], piece[offset-index*r.store.rangeBytes:])
		done += n
		offset += int64(n)
	}
	if done < len(p) {
		return done, io.EOF
	}
	return done, nil
}

// piece gives one piece of the file, from memory or from S3.
func (r *RangeReader) piece(index int64) ([]byte, error) {
	if piece, ok := r.pieces[index]; ok {
		return piece, nil
	}
	start := index * r.store.rangeBytes
	end := min(start+r.store.rangeBytes, r.size)
	// The piece that leaves the memory gives its space to the new piece.
	var piece []byte
	if len(r.order) == keptPieces {
		oldest := r.order[0]
		piece = r.pieces[oldest]
		delete(r.pieces, oldest)
		r.order = r.order[1:]
	}
	if int64(cap(piece)) < end-start {
		piece = make([]byte, end-start)
	}
	piece = piece[:end-start]

	byteRange := fmt.Sprintf("bytes=%d-%d", start, end-1)
	object, err := r.store.client.GetObject(r.ctx, &s3.GetObjectInput{
		Bucket:  aws.String(r.store.bucket),
		Key:     aws.String(r.key),
		Range:   aws.String(byteRange),
		IfMatch: r.etag,
	})
	if err != nil {
		return nil, &Fault{fmt.Errorf("read %s of %q: %w", byteRange, r.key, err)}
	}
	defer object.Body.Close() //nolint:errcheck // The read of the body found each error.
	if _, err := io.ReadFull(object.Body, piece); err != nil {
		return nil, &Fault{fmt.Errorf("read %s of %q: %w", byteRange, r.key, err)}
	}
	r.pieces[index] = piece
	r.order = append(r.order, index)
	return piece, nil
}
