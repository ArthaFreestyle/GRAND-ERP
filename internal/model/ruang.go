package model

type RuangResponse struct {
	ID        int64   `json:"id"`
	Kode      *string `json:"kode,omitempty"`
	NamaRuang string  `json:"nama_ruang"`
	IsAktif   bool    `json:"is_aktif"`

	IDUnitKerja int64 `json:"id_unit_kerja"`
	// NamaUnitKerja is resolved by a join, not a second query.
	NamaUnitKerja string `json:"nama_unit_kerja"`
}

// Kode is optional in the schema but unique when present. IDUnitKerja is
// required and validated against an active unit_kerja (isu #12 fase 2) — a
// room with no unit is a room nobody can decide is theirs to use.
type CreateRuangRequest struct {
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

type ListRuangRequest struct {
	PageRequest
	Search string `query:"search" validate:"omitempty,max=255"`
	// Nil lists every room; set it to filter on is_aktif.
	IsAktif *bool `query:"is_aktif"`

	// AktifIDUnitKerja, same rule as GetRuangRequest.
	AktifIDUnitKerja *int64 `query:"-"`
}
