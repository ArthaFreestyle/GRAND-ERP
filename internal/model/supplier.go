package model

import "time"

type SupplierResponse struct {
	ID      int64   `json:"id"`
	Kode    *string `json:"kode"`
	Nama    string  `json:"nama"`
	Telepon *string `json:"telepon"`
	Alamat  *string `json:"alamat"`
	NPWP    *string `json:"npwp"`
	IsAktif bool    `json:"is_aktif"`

	CreatedAt time.Time `json:"created_at"`
	CreatedBy *int64    `json:"created_by,omitempty"`
	// NamaPembuat is resolved by a join, not a second query.
	NamaPembuat *string   `json:"nama_pembuat,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
	UpdatedBy   *int64    `json:"updated_by,omitempty"`
}

// Kode is optional. When present it is unique case-insensitively
// (supplier_kode_lower_uidx); several suppliers may share kode = NULL, because
// a PostgreSQL unique index does not constrain NULLs.
//
// ActorID is filled from the session by the controller, never from the body —
// the id comes from the verified token, never from anything a caller could set
// to someone else's.
type CreateSupplierRequest struct {
	ActorID int64   `json:"-" validate:"required,gt=0"`
	Kode    *string `json:"kode" validate:"omitempty,max=32"`
	Nama    string  `json:"nama" validate:"required,max=255"`
	Telepon *string `json:"telepon" validate:"omitempty,max=32"`
	Alamat  *string `json:"alamat" validate:"omitempty,max=1000"`
	NPWP    *string `json:"npwp" validate:"omitempty,max=32"`
}

type GetSupplierRequest struct {
	ID int64 `param:"id" validate:"required,gt=0"`
}

// UpdateSupplierRequest patches only the fields present in the body. id,
// created_at, and created_by are deliberately absent — they are never taken
// from a request.
//
// Every nullable column is Optional so `{"telepon": null}` clears it rather than
// being mistaken for "field not sent". Optional tags must lead with omitempty;
// see config.NewValidator.
type UpdateSupplierRequest struct {
	ID      int64            `json:"-" validate:"required,gt=0"`
	ActorID int64            `json:"-" validate:"required,gt=0"`
	Kode    Optional[string] `json:"kode" validate:"omitempty,max=32"`
	Nama    Optional[string] `json:"nama" validate:"omitempty,max=255"`
	Telepon Optional[string] `json:"telepon" validate:"omitempty,max=32"`
	Alamat  Optional[string] `json:"alamat" validate:"omitempty,max=1000"`
	NPWP    Optional[string] `json:"npwp" validate:"omitempty,max=32"`
	IsAktif Optional[bool]   `json:"is_aktif"`
}

type ListSupplierRequest struct {
	PageRequest
	Search string `query:"search" validate:"omitempty,max=255"`
	// Nil lists every supplier; set it to filter on is_aktif.
	IsAktif *bool `query:"is_aktif"`
}
