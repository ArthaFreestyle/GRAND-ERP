package model

import "time"

type PelangganResponse struct {
	ID      int64   `json:"id"`
	Kode    *string `json:"kode"`
	Nama    string  `json:"nama"`
	Telepon *string `json:"telepon"`
	Alamat  *string `json:"alamat"`
	NPWP    *string `json:"npwp"`

	// PlafonKredit is a decimal string, and null means no limit — never zero.
	PlafonKredit *string `json:"plafon_kredit"`

	IsAktif bool `json:"is_aktif"`

	CreatedAt time.Time `json:"created_at"`
	CreatedBy *int64    `json:"created_by,omitempty"`
	// NamaPembuat is resolved by a join, not a second query.
	NamaPembuat *string   `json:"nama_pembuat,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
	UpdatedBy   *int64    `json:"updated_by,omitempty"`
}

// Kode is optional. When present it is unique case-insensitively
// (pelanggan_kode_lower_uidx); several customers may share kode = NULL.
//
// PlafonKredit is a string so no float ever touches money. Omitting it, or
// sending null, means unlimited credit.
//
// ActorID is filled from the session by the controller, never from the body —
// the id comes from the verified token, never from anything a caller could set
// to someone else's.
type CreatePelangganRequest struct {
	ActorID      int64   `json:"-" validate:"required,gt=0"`
	Kode         *string `json:"kode" validate:"omitempty,max=32"`
	Nama         string  `json:"nama" validate:"required,max=255"`
	Telepon      *string `json:"telepon" validate:"omitempty,max=32"`
	Alamat       *string `json:"alamat" validate:"omitempty,max=1000"`
	NPWP         *string `json:"npwp" validate:"omitempty,max=32"`
	PlafonKredit *string `json:"plafon_kredit" validate:"omitempty,numeric,max=21"`
}

type GetPelangganRequest struct {
	ID int64 `param:"id" validate:"required,gt=0"`
}

// UpdatePelangganRequest patches only the fields present in the body. id,
// created_at, and created_by are deliberately absent.
//
// PlafonKredit is Optional because null is meaningful here: it lifts the credit
// limit entirely, which a plain pointer could not express.
type UpdatePelangganRequest struct {
	ID           int64            `json:"-" validate:"required,gt=0"`
	ActorID      int64            `json:"-" validate:"required,gt=0"`
	Kode         Optional[string] `json:"kode" validate:"omitempty,max=32"`
	Nama         Optional[string] `json:"nama" validate:"omitempty,max=255"`
	Telepon      Optional[string] `json:"telepon" validate:"omitempty,max=32"`
	Alamat       Optional[string] `json:"alamat" validate:"omitempty,max=1000"`
	NPWP         Optional[string] `json:"npwp" validate:"omitempty,max=32"`
	PlafonKredit Optional[string] `json:"plafon_kredit" validate:"omitempty,numeric,max=21"`
	IsAktif      Optional[bool]   `json:"is_aktif"`
}

type ListPelangganRequest struct {
	PageRequest
	Search string `query:"search" validate:"omitempty,max=255"`
	// Nil lists every customer; set it to filter on is_aktif.
	IsAktif *bool `query:"is_aktif"`
}
