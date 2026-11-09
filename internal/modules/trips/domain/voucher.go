package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Errors of vouchers.
var (
	ErrVoucherNotFound = errors.New("voucher not found")
	// ErrVoucherSettled: a voucher already discounted from a payment cannot change.
	ErrVoucherSettled = errors.New("voucher already settled")
	// ErrVouchersExceed: the vouchers chosen are worth more than the trips paid.
	ErrVouchersExceed = errors.New("vouchers exceed the trips total")
	// ErrNoVoucherPhoto: the voucher has no photo (or it was already cleaned up).
	ErrNoVoucherPhoto = errors.New("voucher photo not found")
)

// maxVoucherAmount: no single fill-up costs ten thousand soles.
var maxVoucherAmount = decimal.NewFromInt(10_000)

// VoucherPhoto is the receipt's photo metadata; the file lives in the photo store.
type VoucherPhoto struct {
	StorageKey  string
	ContentType string
	SizeBytes   int64
	CreatedAt   time.Time
}

// Voucher is fuel the borrower paid for while using the car. Until it is
// settled it is credit: it lowers what the borrower owes.
type Voucher struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	Date        time.Time
	Amount      decimal.Decimal
	SettledDate *time.Time
	Photo       *VoucherPhoto
	CreatedAt   time.Time
}

// Settled tells whether the voucher was already discounted from a payment.
func (v Voucher) Settled() bool { return v.SettledDate != nil }

// ValidateVoucherAmount checks what the voucher says: more than zero, in cents.
func ValidateVoucherAmount(amount decimal.Decimal, problems *ValidationError) {
	switch {
	case !amount.IsPositive():
		problems.Add("amount", CodeNotPositive)
	case amount.GreaterThan(maxVoucherAmount):
		problems.Add("amount", CodeTooLarge)
	case !amount.Equal(amount.Round(2)):
		problems.Add("amount", CodeTooPrecise)
	}
}

// Balance is what the borrower still owes: the trips minus the vouchers. It is
// negative when the vouchers are worth more than the trips.
func Balance(trips, vouchers decimal.Decimal) decimal.Decimal { return trips.Sub(vouchers) }
