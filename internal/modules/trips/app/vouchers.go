package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/AtmosferaSoltec/atm-km-api/internal/integrations/photostore"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/trips/domain"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/ids"
)

// MaxVouchers caps how many vouchers one list or one payment handles.
const MaxVouchers = 200

// VoucherInput is a voucher as the client sends it.
type VoucherInput struct {
	Date   string
	Amount *decimal.Decimal
}

// AddVoucher registers a voucher the borrower paid.
func (s *Service) AddVoucher(ctx context.Context, userID uuid.UUID, in VoucherInput) (domain.Voucher, error) {
	var problems domain.ValidationError
	voucher := domain.Voucher{ID: ids.New(), UserID: userID, CreatedAt: s.deps.Clock.Now()}
	voucher.Date = parseDate("date", in.Date, &problems)
	if !voucher.Date.IsZero() {
		domain.ValidateDate("date", voucher.Date, s.today(), &problems)
	}
	if in.Amount == nil {
		problems.Add("amount", domain.CodeRequired)
	} else {
		voucher.Amount = *in.Amount
		domain.ValidateVoucherAmount(voucher.Amount, &problems)
	}
	if err := problems.OrNil(); err != nil {
		return domain.Voucher{}, err
	}
	if err := s.deps.Vouchers.Insert(ctx, voucher); err != nil {
		return domain.Voucher{}, err
	}
	return voucher, nil
}

// Vouchers lists the unsettled (credit) or the settled vouchers, newest first.
func (s *Service) Vouchers(ctx context.Context, userID uuid.UUID, settled bool) ([]domain.Voucher, error) {
	return s.deps.Vouchers.List(ctx, userID, settled, MaxVouchers)
}

// DeleteVoucher removes an unsettled voucher and its photo.
func (s *Service) DeleteVoucher(ctx context.Context, userID, id uuid.UUID) error {
	var photo *domain.VoucherPhoto
	err := s.deps.Tx.InTx(ctx, func(ctx context.Context) error {
		voucher, err := s.deps.Vouchers.Lock(ctx, userID, id)
		if err != nil {
			return err
		}
		if voucher.Settled() {
			return domain.ErrVoucherSettled
		}
		photo = voucher.Photo
		return s.deps.Vouchers.Delete(ctx, userID, id)
	})
	if err != nil {
		return err
	}
	if photo != nil {
		s.deleteFile(ctx, photo.StorageKey)
	}
	return nil
}

// SetVoucherPhoto stores (or replaces) the voucher's receipt photo.
func (s *Service) SetVoucherPhoto(ctx context.Context, userID, id uuid.UUID, data []byte) (domain.Voucher, error) {
	image, err := checkPhoto(data)
	if err != nil {
		return domain.Voucher{}, err
	}
	// Checked before writing the file, and again under the lock.
	voucher, err := s.deps.Vouchers.Get(ctx, userID, id)
	if err != nil {
		return domain.Voucher{}, err
	}
	if voucher.Settled() {
		return domain.Voucher{}, domain.ErrVoucherSettled
	}

	photo := domain.VoucherPhoto{ContentType: image.ContentType, SizeBytes: int64(len(data)), CreatedAt: s.deps.Clock.Now()}
	photo.StorageKey = fmt.Sprintf("%s/%s.%s", userID, ids.New(), image.Extension)
	if err := s.deps.Files.Save(photo.StorageKey, data); err != nil {
		return domain.Voucher{}, err
	}

	var replaced *domain.VoucherPhoto
	err = s.deps.Tx.InTx(ctx, func(ctx context.Context) error {
		locked, err := s.deps.Vouchers.Lock(ctx, userID, id)
		if err != nil {
			return err
		}
		if locked.Settled() {
			return domain.ErrVoucherSettled
		}
		replaced = locked.Photo
		return s.deps.Vouchers.SetPhoto(ctx, userID, id, &photo)
	})
	if err != nil {
		s.deleteFile(ctx, photo.StorageKey)
		return domain.Voucher{}, err
	}
	if replaced != nil {
		s.deleteFile(ctx, replaced.StorageKey)
	}
	return s.deps.Vouchers.Get(ctx, userID, id)
}

// DeleteVoucherPhoto removes the voucher's photo; the voucher stays.
func (s *Service) DeleteVoucherPhoto(ctx context.Context, userID, id uuid.UUID) (domain.Voucher, error) {
	var removed *domain.VoucherPhoto
	err := s.deps.Tx.InTx(ctx, func(ctx context.Context) error {
		voucher, err := s.deps.Vouchers.Lock(ctx, userID, id)
		if err != nil {
			return err
		}
		if voucher.Settled() {
			return domain.ErrVoucherSettled
		}
		if voucher.Photo == nil {
			return domain.ErrNoVoucherPhoto
		}
		removed = voucher.Photo
		return s.deps.Vouchers.SetPhoto(ctx, userID, id, nil)
	})
	if err != nil {
		return domain.Voucher{}, err
	}
	s.deleteFile(ctx, removed.StorageKey)
	return s.deps.Vouchers.Get(ctx, userID, id)
}

// OpenVoucherPhoto returns a voucher photo's file to serve. The caller already
// checked the signed URL, which is what authorizes it.
func (s *Service) OpenVoucherPhoto(ctx context.Context, id uuid.UUID) (domain.VoucherPhoto, *os.File, error) {
	voucher, err := s.deps.Vouchers.GetByID(ctx, id)
	if err != nil {
		return domain.VoucherPhoto{}, nil, domain.ErrNoVoucherPhoto
	}
	if voucher.Photo == nil {
		return domain.VoucherPhoto{}, nil, domain.ErrNoVoucherPhoto
	}
	file, err := s.deps.Files.Open(voucher.Photo.StorageKey)
	if errors.Is(err, photostore.ErrNotFound) {
		return domain.VoucherPhoto{}, nil, domain.ErrNoVoucherPhoto
	}
	if err != nil {
		return domain.VoucherPhoto{}, nil, err
	}
	return *voucher.Photo, file, nil
}

// VoucherPhotoExpiresAt is when the cleanup deletes the voucher's photo.
func (s *Service) VoucherPhotoExpiresAt(photo domain.VoucherPhoto) time.Time {
	return photo.CreatedAt.Add(s.deps.PhotoRetention)
}

// cleanupVoucherPhotos clears the photos older than the cutoff and returns how many.
func (s *Service) cleanupVoucherPhotos(ctx context.Context, cutoff time.Time) (int, error) {
	count := 0
	for {
		expired, err := s.deps.Vouchers.ExpiredPhotos(ctx, cutoff, cleanupBatch)
		if err != nil {
			return count, err
		}
		for _, voucher := range expired {
			if err := s.deps.Vouchers.ClearPhoto(ctx, voucher.ID); err != nil {
				return count, err
			}
			s.deleteFile(ctx, voucher.Photo.StorageKey)
			count++
		}
		if len(expired) < cleanupBatch {
			return count, nil
		}
	}
}
