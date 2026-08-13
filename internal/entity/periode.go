package entity

import "time"

// Status values for periode. The CHECK constraint in migration 000002 accepts
// exactly these two.
const (
	StatusPeriodeBuka  = "BUKA"
	StatusPeriodeTutup = "TUTUP"
)

// Periode maps the periode table: one row per closed month.
//
// The identity is (tahun, bulan), not id — periode_tahun_bulan_uidx says so, and
// every endpoint is keyed that way. The id column exists but nothing outside this
// package has any use for it.
//
// A month with no row counts as open. That is a decision from migration 000004, so
// that a fresh database is not jammed shut before anyone has typed anything, and it
// is why closing a month means *creating* its row rather than updating one.
// PeriodeUseCase.Get therefore answers a synthetic BUKA for a month that has never
// been touched — the same shape as a stored row, with the four audit fields empty.
type Periode struct {
	ID     int64
	Tahun  int
	Bulan  int
	Status string

	// Who closed it and when. Only the last closing: reopening and closing again
	// overwrites both, which is exactly why the pair below exists.
	DitutupOleh *int64
	TsTutup     *time.Time

	// Who reopened it and when, added by migration 000017. Without this, a month
	// that was closed, reopened, and closed again looks identical to one that was
	// only ever closed.
	DibukaOleh *int64
	TsBuka     *time.Time

	// Resolved by LEFT JOINs on users, not by a query per row.
	NamaPenutup *string
	NamaPembuka *string
}
