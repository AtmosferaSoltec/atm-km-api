package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/AtmosferaSoltec/atm-km-api/internal/integrations/photostore"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/trips/domain"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/ids"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/logger"
)

// MaxPhotoBytes: the web compresses to ~300 KB; a raw phone photo still fits.
const MaxPhotoBytes = 10 << 20

// cleanupBatch is how many expired photos one pass deletes per query.
const cleanupBatch = 200

// SetPhoto stores (or replaces) a leg's odometer photo. The end photo needs the
// car to be back; a cancelled trip takes no photos.
func (s *Service) SetPhoto(ctx context.Context, userID, tripID uuid.UUID, leg domain.Leg, data []byte) (domain.Trip, error) {
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
	if err := problems.OrNil(); err != nil {
		return domain.Trip{}, err
	}

	// Checked before writing the file, and again under the lock.
	trip, err := s.deps.Trips.Get(ctx, userID, tripID)
	if err != nil {
		return domain.Trip{}, err
	}
	if err := photoAllowed(trip, leg); err != nil {
		return domain.Trip{}, err
	}

	photo := domain.Photo{
		ID: ids.New(), TripID: tripID, UserID: userID, Leg: leg,
		ContentType: image.ContentType, SizeBytes: int64(len(data)), CreatedAt: s.deps.Clock.Now(),
	}
	photo.StorageKey = fmt.Sprintf("%s/%s.%s", userID, photo.ID, image.Extension)
	if err := s.deps.Files.Save(photo.StorageKey, data); err != nil {
		return domain.Trip{}, err
	}

	var replaced *domain.Photo
	err = s.deps.Tx.InTx(ctx, func(ctx context.Context) error {
		trip, err := s.deps.Trips.Lock(ctx, userID, tripID)
		if err != nil {
			return err
		}
		if err := photoAllowed(trip, leg); err != nil {
			return err
		}
		old, err := s.deps.Photos.OfLeg(ctx, tripID, leg)
		switch {
		case err == nil:
			replaced = &old
			if err := s.deps.Photos.Delete(ctx, old.ID); err != nil {
				return err
			}
		case !errors.Is(err, domain.ErrNoPhoto):
			return err
		}
		return s.deps.Photos.Insert(ctx, photo)
	})
	if err != nil {
		s.deleteFile(ctx, photo.StorageKey)
		return domain.Trip{}, err
	}
	if replaced != nil {
		s.deleteFile(ctx, replaced.StorageKey)
	}
	return s.Get(ctx, userID, tripID)
}

func photoAllowed(trip domain.Trip, leg domain.Leg) error {
	switch {
	case trip.Status == domain.StatusCancelled:
		return domain.ErrWrongStatus
	case leg == domain.LegEnd && trip.Return == nil:
		return domain.ErrPhotoLegClosed
	}
	return nil
}

// DeletePhoto removes a leg's photo.
func (s *Service) DeletePhoto(ctx context.Context, userID, tripID uuid.UUID, leg domain.Leg) (domain.Trip, error) {
	var removed domain.Photo
	err := s.deps.Tx.InTx(ctx, func(ctx context.Context) error {
		if _, err := s.deps.Trips.Lock(ctx, userID, tripID); err != nil {
			return err
		}
		photo, err := s.deps.Photos.OfLeg(ctx, tripID, leg)
		if err != nil {
			return err
		}
		removed = photo
		return s.deps.Photos.Delete(ctx, photo.ID)
	})
	if err != nil {
		return domain.Trip{}, err
	}
	s.deleteFile(ctx, removed.StorageKey)
	return s.Get(ctx, userID, tripID)
}

// OpenPhoto returns a photo's file to serve. The caller already checked the
// signed URL, which is what authorizes it.
func (s *Service) OpenPhoto(ctx context.Context, photoID uuid.UUID) (domain.Photo, *os.File, error) {
	photo, err := s.deps.Photos.Get(ctx, photoID)
	if err != nil {
		return domain.Photo{}, nil, err
	}
	file, err := s.deps.Files.Open(photo.StorageKey)
	if errors.Is(err, photostore.ErrNotFound) {
		return domain.Photo{}, nil, domain.ErrNoPhoto
	}
	if err != nil {
		return domain.Photo{}, nil, err
	}
	return photo, file, nil
}

// PhotoExpiresAt is when the cleanup deletes the photo.
func (s *Service) PhotoExpiresAt(photo domain.Photo) time.Time {
	return photo.CreatedAt.Add(s.deps.PhotoRetention)
}

// CleanupResult says what a cleanup pass deleted.
type CleanupResult struct {
	Photos int
	Orphan int
}

// CleanupPhotos deletes photos older than the retention, rows and files, and
// then any file left on disk past the retention (a crash between deleting the
// row and the file). Photos are evidence for a while, not an archive.
func (s *Service) CleanupPhotos(ctx context.Context) (CleanupResult, error) {
	cutoff := s.deps.Clock.Now().Add(-s.deps.PhotoRetention)
	var result CleanupResult
	for {
		expired, err := s.deps.Photos.Expired(ctx, cutoff, cleanupBatch)
		if err != nil {
			return result, err
		}
		for _, photo := range expired {
			if err := s.deps.Photos.Delete(ctx, photo.ID); err != nil {
				return result, err
			}
			s.deleteFile(ctx, photo.StorageKey)
			result.Photos++
		}
		if len(expired) < cleanupBatch {
			break
		}
	}
	// A day of margin: a file being uploaded right now is never swept.
	orphans, err := s.deps.Files.SweepOlderThan(cutoff.Add(-24 * time.Hour))
	result.Orphan = orphans
	if err != nil {
		return result, err
	}
	if result.Photos > 0 || result.Orphan > 0 {
		logger.From(ctx).Info("photos cleaned up", "photos", result.Photos, "orphan_files", result.Orphan)
	}
	return result, nil
}
