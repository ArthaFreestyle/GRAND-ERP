package model

import "time"

type UnitKerjaResponse struct {
	ID      int64   `json:"id"`
	Kode    *string `json:"kode"`
	Nama    string  `json:"nama"`
	IsAktif bool    `json:"is_aktif"`

	CreatedAt time.Time `json:"created_at"`
	CreatedBy *int64    `json:"created_by,omitempty"`
	// NamaPembuat is resolved by a join, not a second query.
	NamaPembuat *string   `json:"nama_pembuat,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
	UpdatedBy   *int64    `json:"updated_by,omitempty"`
}

// Kode is optional. When present it is unique case-insensitively
// (unit_kerja_kode_lower_uidx); several units may share kode = NULL, because a
// PostgreSQL unique index does not constrain NULLs.
type CreateUnitKerjaRequest struct {
	Kode *string `json:"kode" validate:"omitempty,max=32"`
	Nama string  `json:"nama" validate:"required,max=255"`
}

type GetUnitKerjaRequest struct {
	ID int64 `param:"id" validate:"required,gt=0"`
}

// UpdateUnitKerjaRequest patches only the fields present in the body. id,
// created_at, and created_by are deliberately absent — they are never taken
// from a request.
//
// Optional tags must lead with omitempty; see config.NewValidator.
type UpdateUnitKerjaRequest struct {
	ID      int64            `json:"-" validate:"required,gt=0"`
	Kode    Optional[string] `json:"kode" validate:"omitempty,max=32"`
	Nama    Optional[string] `json:"nama" validate:"omitempty,max=255"`
	IsAktif Optional[bool]   `json:"is_aktif"`
}

type ListUnitKerjaRequest struct {
	PageRequest
	Search string `query:"search" validate:"omitempty,max=255"`
	// Nil lists every unit; set it to filter on is_aktif.
	IsAktif *bool `query:"is_aktif"`
}
