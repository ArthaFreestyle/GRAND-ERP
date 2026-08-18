package model

import "time"

type EkspedisiResponse struct {
	ID        int64     `json:"id"`
	Nama      string    `json:"nama"`
	Telepon   *string   `json:"telepon"`
	IsAktif   bool      `json:"is_aktif"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy *int64    `json:"created_by,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
	UpdatedBy *int64    `json:"updated_by,omitempty"`
}

// Nama is unique case-insensitively since migration 000009
// (ekspedisi_nama_lower_uidx).
//
// ActorID is filled from the session by the controller, never from the body —
// the id comes from the verified token, never from anything a caller could set
// to someone else's.
type CreateEkspedisiRequest struct {
	ActorID int64   `json:"-" validate:"required,gt=0"`
	Nama    string  `json:"nama" validate:"required,max=255"`
	Telepon *string `json:"telepon" validate:"omitempty,max=32"`
}

type GetEkspedisiRequest struct {
	ID int64 `param:"id" validate:"required,gt=0"`
}

// UpdateEkspedisiRequest patches only the fields present in the body. id,
// created_at, and created_by are deliberately absent.
//
// Telepon is Optional so `{"telepon": null}` clears the number instead of being
// mistaken for "field not sent".
type UpdateEkspedisiRequest struct {
	ID      int64            `json:"-" validate:"required,gt=0"`
	ActorID int64            `json:"-" validate:"required,gt=0"`
	Nama    Optional[string] `json:"nama" validate:"omitempty,max=255"`
	Telepon Optional[string] `json:"telepon" validate:"omitempty,max=32"`
	IsAktif Optional[bool]   `json:"is_aktif"`
}

type ListEkspedisiRequest struct {
	PageRequest
	Search string `query:"search" validate:"omitempty,max=255"`
	// Nil lists every carrier; set it to filter on is_aktif.
	IsAktif *bool `query:"is_aktif"`
}
