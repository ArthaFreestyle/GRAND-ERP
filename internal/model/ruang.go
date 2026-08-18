package model

import "time"

type RuangResponse struct {
	ID        int64   `json:"id"`
	Kode      *string `json:"kode,omitempty"`
	NamaRuang string  `json:"nama_ruang"`
	IsAktif   bool    `json:"is_aktif"`

	IDUnitKerja int64 `json:"id_unit_kerja"`
	// NamaUnitKerja is resolved by a join, not a second query.
	NamaUnitKerja string `json:"nama_unit_kerja"`

	CreatedAt time.Time `json:"created_at"`
	CreatedBy *int64    `json:"created_by,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
	UpdatedBy *int64    `json:"updated_by,omitempty"`

	// NomorOpnameBeku is the nomor of the stok_opname currently freezing this
	// room, null when it is free — isu #15. A caller whose posting was refused
	// can see the cause and who to chase right here, without a second call.
	NomorOpnameBeku *string `json:"nomor_opname_beku"`
}

// Kode is optional in the schema but unique when present. IDUnitKerja is
// required and validated against an active unit_kerja (isu #12 fase 2) — a
// room with no unit is a room nobody can decide is theirs to use.
//
// ActorID is filled from the session by the controller, never from the body —
// the id comes from the verified token, never from anything a caller could set
// to someone else's.
type CreateRuangRequest struct {
	ActorID     int64   `json:"-" validate:"required,gt=0"`
	Kode        *string `json:"kode" validate:"omitempty,max=32"`
	NamaRuang   string  `json:"nama_ruang" validate:"required,max=255"`
	IDUnitKerja int64   `json:"id_unit_kerja" validate:"required,gt=0"`
}

type GetRuangRequest struct {
	ID int64 `param:"id" validate:"required,gt=0"`

	// AktifIDUnitKerja is filled from the session's active grant by the
	// controller, never from the query — isu #12 fase 6. Nil means the active
	// grant applies everywhere, so nothing is excluded; otherwise a room
	// outside this unit answers 404, the same as one that does not exist.
	AktifIDUnitKerja *int64 `query:"-"`
}

// UpdateRuangRequest patches only the fields present in the body. id,
// created_at, and created_by are deliberately absent — they are never taken
// from a request.
//
// id_unit_kerja is deliberately absent too, and not for imutability's own sake
// the way kode_barang is on product: kartu_stok is partitioned by
// (id_barang, id_ruang), and isu #12 fase 6's read scoping reads a room's unit
// straight off ruang.id_unit_kerja. Moving a ruang to another unit through this
// endpoint would carry its whole history and balance across with it, with no
// kartu_stok row ever changing — the old unit's reports would lose stock that
// was really there, and the new one would gain stock that never arrived
// through any document. Goods that actually moved are a mutasi into a room the
// destination unit owns; a ruang that was assigned to the wrong unit is retired
// and a new one created in its place.
type UpdateRuangRequest struct {
	ID        int64            `json:"-" validate:"required,gt=0"`
	ActorID   int64            `json:"-" validate:"required,gt=0"`
	Kode      Optional[string] `json:"kode" validate:"omitempty,max=32"`
	NamaRuang Optional[string] `json:"nama_ruang" validate:"omitempty,max=255"`
	IsAktif   Optional[bool]   `json:"is_aktif"`
}

type ListRuangRequest struct {
	PageRequest
	Search string `query:"search" validate:"omitempty,max=255"`
	// Nil lists every room; set it to filter on is_aktif.
	IsAktif *bool `query:"is_aktif"`

	// AktifIDUnitKerja, same rule as GetRuangRequest.
	AktifIDUnitKerja *int64 `query:"-"`
}
