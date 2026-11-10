package app

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/google/uuid"

	"github.com/AtmosferaSoltec/atm-km-api/internal/integrations/photostore"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/fuel/domain"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/ids"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/logger"
)

// MaxPhotoBytes: the web compresses to ~300 KB; a raw phone photo still fits.
const MaxPhotoBytes = 10 << 20

// cleanupBatch is how many expired photos one pass deletes per query.
const cleanupBatch = 200

// Files keeps the photo files (integrations/photostore).
type Files interface {
	Save(key string, data []byte) error
	Open(key string) (*os.File, error)
	Delete(key string) error
}

// Transactor opens a transaction (platform/tx.Runner).
type Transactor interface {
	InTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// SetPhoto stores (or replaces) the fill-up's receipt photo.
func (s *Service) SetPhoto(ctx context.Context, userID, id uuid.UUID, data []byte) (domain.Log, error) {
	image, err := checkPhoto(data)
	if err != nil {
		return domain.Log{}, err
	}
	// Checked before writing the file, so a stranger's id writes nothing to disk.
	if _, err := s.deps.Logs.Get(ctx, userID, id); err != nil {
		return domain.Log{}, err
	}

	photo := domain.Photo{ContentType: image.ContentType, SizeBytes: int64(len(data)), CreatedAt: s.deps.Clock.Now()}
	photo.StorageKey = fmt.Sprintf("%s/%s.%s", userID, ids.New(), image.Extension)
	if err := s.deps.Files.Save(photo.StorageKey, data); err != nil {
		return domain.Log{}, err
	}

	var replaced *domain.Photo
	err = s.deps.Tx.InTx(ctx, func(ctx context.Context) error {
		locked, err := s.deps.Logs.Lock(ctx, userID, id)
		if err != nil {
			return err
		}
		replaced = locked.Photo
		return s.deps.Logs.SetPhoto(ctx, userID, id, &photo)
	})
	if err != nil {
		s.deleteFile(ctx, photo.StorageKey)
		return domain.Log{}, err
	}
	if replaced != nil {
		s.deleteFile(ctx, replaced.StorageKey)
	}
	return s.deps.Logs.Get(ctx, userID, id)
}

// DeletePhoto removes the fill-up's photo; the fill-up stays.
func (s *Service) DeletePhoto(ctx context.Context, userID, id uuid.UUID) (domain.Log, error) {
	var removed *domain.Photo
	err := s.deps.Tx.InTx(ctx, func(ctx context.Context) error {
		log, err := s.deps.Logs.Lock(ctx, userID, id)
		if err != nil {
			return err
		}
		if log.Photo == nil {
			return domain.ErrNoPhoto
		}
		removed = log.Photo
		return s.deps.Logs.SetPhoto(ctx, userID, id, nil)
	})
	if err != nil {
		return domain.Log{}, err
	}
	s.deleteFile(ctx, removed.StorageKey)
	return s.deps.Logs.Get(ctx, userID, id)
}

// OpenPhoto returns a photo's file to serve. The caller already checked the
// signed URL, which is what authorizes it.
func (s *Service) OpenPhoto(ctx context.Context, id uuid.UUID) (domain.Photo, *os.File, error) {
	log, err := s.deps.Logs.GetByID(ctx, id)
	if err != nil || log.Photo == nil {
		return domain.Photo{}, nil, domain.ErrNoPhoto
	}
	file, err := s.deps.Files.Open(log.Photo.StorageKey)
	if errors.Is(err, photostore.ErrNotFound) {
		return domain.Photo{}, nil, domain.ErrNoPhoto
	}
	if err != nil {
		return domain.Photo{}, nil, err
	}
	return *log.Photo, file, nil
}

// CleanupPhotos clears the photos older than the retention, rows and files, and
// returns how many. The fill-ups stay.
func (s *Service) CleanupPhotos(ctx context.Context) (int, error) {
	cutoff := s.deps.Clock.Now().Add(-s.deps.PhotoRetention)
	count := 0
	for {
		expired, err := s.deps.Logs.ExpiredPhotos(ctx, cutoff, cleanupBatch)
		if err != nil {
			return count, err
		}
		for _, log := range expired {
			if err := s.deps.Logs.ClearPhoto(ctx, log.ID); err != nil {
				return count, err
			}
			s.deleteFile(ctx, log.Photo.StorageKey)
			count++
		}
		if len(expired) < cleanupBatch {
			break
		}
	}
	if count > 0 {
		logger.From(ctx).Info("fuel photos cleaned up", "photos", count)
	}
	return count, nil
}

func (s *Service) deleteFile(ctx context.Context, key string) {
	if err := s.deps.Files.Delete(key); err != nil {
		// The trips cleanup's sweep removes it later.
		logger.From(ctx).Warn("photo file not deleted", "error", err)
	}
}

// checkPhoto validates an uploaded photo and says what kind of image it is.
func checkPhoto(data []byte) (photostore.Image, error) {
	var problems domain.ValidationError
	image, ok := photostore.Detect(data)
	switch {
	case len(data) == 0:
		problems.Add("photo", domain.CodeEmpty)
	case len(data) > MaxPhotoBytes:
		problems.Add("photo", domain.CodeTooLarge)
	case !ok:
		problems.Add("photo", domain.CodeUnsupported)
	}
	return image, problems.OrNil()
}
