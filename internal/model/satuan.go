package model

import "time"

type SatuanResponse struct {
	ID        int64     `json:"id"`
	Nama      string    `json:"nama"`
	IsAktif   bool      `json:"is_aktif"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy *int64    `json:"created_by,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
	UpdatedBy *int64    `json:"updated_by,omitempty"`
}

// Nama is unique case-insensitively (satuan_nama_lower_uidx), so "Pcs" and
// "PCS" collide.
//
// ActorID is filled from the session by the controller, never from the body —
// the id comes from the verified token, never from anything a caller could set
// to someone else's.
type CreateSatuanRequest struct {
	ActorID int64  `json:"-" validate:"required,gt=0"`
	Nama    string `json:"nama" validate:"required,max=64"`
}

type GetSatuanRequest struct {
	ID int64 `param:"id" validate:"required,gt=0"`
}

// UpdateSatuanRequest patches only the fields present in the body. id,
// created_at, and created_by are deliberately absent — they are never taken
// from a request.
//
// Optional tags must lead with omitempty; see config.NewValidator.
type UpdateSatuanRequest struct {
	ID      int64            `json:"-" validate:"required,gt=0"`
	ActorID int64            `json:"-" validate:"required,gt=0"`
	Nama    Optional[string] `json:"nama" validate:"omitempty,max=64"`
	IsAktif Optional[bool]   `json:"is_aktif"`
}

type ListSatuanRequest struct {
	PageRequest
	Search string `query:"search" validate:"omitempty,max=255"`
	// Nil lists every unit; set it to filter on is_aktif.
	IsAktif *bool `query:"is_aktif"`
}
