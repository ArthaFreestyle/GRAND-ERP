package model

import "time"

// PeriodeResponse carries one month's book-closing state.
//
// There is deliberately no id. A periode is identified by (tahun, bulan) — that is
// what periode_tahun_bulan_uidx declares and what every route is keyed on — and
// leaving the surrogate key out means a month that has never been closed answers in
// exactly the same shape as one that has, rather than needing a null id or an
// invented zero.
type PeriodeResponse struct {
	Tahun  int    `json:"tahun"`
	Bulan  int    `json:"bulan"`
	Status string `json:"status"`

	DitutupOleh *int64     `json:"ditutup_oleh,omitempty"`
	NamaPenutup *string    `json:"nama_penutup,omitempty"`
	TsTutup     *time.Time `json:"ts_tutup,omitempty"`

	// Filled only when the month was reopened after a closing. Present alongside
	// ts_tutup means it was closed, reopened, and closed again.
	DibukaOleh  *int64     `json:"dibuka_oleh,omitempty"`
	NamaPembuka *string    `json:"nama_pembuka,omitempty"`
	TsBuka      *time.Time `json:"ts_buka,omitempty"`
}

// GetPeriodeRequest addresses one month.
//
// tahun is bounded rather than left open because it is typed into a URL: a typo
// producing year 20260 is worth a 400 rather than a page of nothing. bulan is bounded
// by periode_bulan_check anyway, and validating it here names the field instead of a
// constraint.
type GetPeriodeRequest struct {
	Tahun int `param:"tahun" validate:"required,min=2000,max=9999"`
	Bulan int `param:"bulan" validate:"required,min=1,max=12"`
}

// TutupPeriodeRequest closes a month. There is no body: what is being asked is
// entirely in the path, and who is asking comes from the token.
type TutupPeriodeRequest struct {
	Tahun int `json:"-" validate:"required,min=2000,max=9999"`
	Bulan int `json:"-" validate:"required,min=1,max=12"`
	// ActorID is filled from the session by the controller, never from the body.
	// A book closing whose author is whoever the request claimed to be is not a
	// record of anything.
	ActorID int64 `json:"-" validate:"required,gt=0"`
}

// BukaPeriodeRequest reopens a closed month. Same shape as closing it, and the same
// role behind it.
type BukaPeriodeRequest struct {
	Tahun   int   `json:"-" validate:"required,min=2000,max=9999"`
	Bulan   int   `json:"-" validate:"required,min=1,max=12"`
	ActorID int64 `json:"-" validate:"required,gt=0"`
}

// ListPeriodeRequest pages over the stored rows.
//
// Note what this cannot answer: months that were never closed have no row, so they
// are absent from the list however open they are. That is the same decision as
// everywhere else in this module — the table records closings, not calendars. Ask
// about one specific month through GET /periode/{tahun}/{bulan}, which answers for
// every month whether or not a row exists.
type ListPeriodeRequest struct {
	PageRequest
	// Zero lists every year.
	Tahun int `query:"tahun" validate:"omitempty,min=2000,max=9999"`
	// Empty lists both statuses.
	Status string `query:"status" validate:"omitempty,oneof=BUKA TUTUP"`
}
