package documents

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/Njunge11/rejea/internal/database"
	"github.com/Njunge11/rejea/internal/queue"
)

// RecordHash saves the hash of the file of a document. When a different
// document has the same hash, the two files have the same content: the
// document then gets the status duplicate and no hash, and RecordHash
// reports true.
func (s *Service) RecordHash(ctx context.Context, id uuid.UUID, hash string) (duplicate bool, err error) {
	saved, err := database.SetFileHash(ctx, s.pool, id, hash)
	if err != nil || saved {
		return false, err
	}
	if err := database.SetStatus(ctx, s.pool, id, StatusDuplicate); err != nil {
		return false, err
	}
	return true, nil
}

// UnpackedFile is a file that came out of an archive and is in the file
// storage.
type UnpackedFile struct {
	Filename   string
	StorageKey string
	// Hash is the hash of the content.
	Hash string
}

// SaveUnpacked makes one document for each file that came out of an
// archive, and gives the archive the status unpacked. It gives the number
// of the new documents that are not duplicates.
//
// Each document points to the archive and gets its uploader and its
// metadata. A file with the same content as the file of a different
// document, also of a file from the same archive, becomes a duplicate.
// Each other document gets a file job. The documents, their jobs and the
// status of the archive change together or not at all.
func (s *Service) SaveUnpacked(ctx context.Context, archiveID uuid.UUID, files []UnpackedFile) (int, error) {
	archive, err := database.GetDocument(ctx, s.pool, archiveID)
	if err != nil {
		return 0, err
	}
	docs := make([]Document, len(files))
	for i, file := range files {
		id, err := newID()
		if err != nil {
			return 0, err
		}
		docs[i] = Document{
			ID:         id,
			UploadedBy: archive.UploadedBy,
			Filename:   file.Filename,
			StorageKey: file.StorageKey,
			Metadata:   archive.Metadata,
			Status:     StatusUploaded,
			FileHash:   file.Hash,
			ParentID:   archiveID,
		}
	}
	var jobs []river.InsertManyParams
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		jobs = jobs[:0]
		added, err := database.InsertDocuments(ctx, tx, docs)
		if err != nil {
			return err
		}
		var duplicates []Document
		for i, doc := range docs {
			if added[i] {
				jobs = append(jobs, river.InsertManyParams{Args: queue.FileArgs{DocumentID: doc.ID}})
				continue
			}
			// A different document has this hash.
			doc.Status = StatusDuplicate
			doc.FileHash = ""
			duplicates = append(duplicates, doc)
		}
		if len(duplicates) > 0 {
			if _, err := database.InsertDocuments(ctx, tx, duplicates); err != nil {
				return err
			}
		}
		if err := database.SetStatus(ctx, tx, archiveID, StatusUnpacked); err != nil {
			return err
		}
		if len(jobs) == 0 {
			return nil
		}
		if _, err := s.jobs.InsertManyTx(ctx, tx, jobs); err != nil {
			return fmt.Errorf("add the file jobs of the archive %s: %w", archiveID, err)
		}
		return nil
	})
	return len(jobs), err
}
