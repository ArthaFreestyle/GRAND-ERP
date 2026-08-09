package model

type RuangResponse struct {
	ID        int64   `json:"id"`
	Kode      *string `json:"kode,omitempty"`
	NamaRuang string  `json:"nama_ruang"`
	IsAktif   bool    `json:"is_aktif"`
}

// Kode is optional in the schema but unique when present.
type CreateRuangRequest struct {
	Kode      *string `json:"kode" validate:"omitempty,max=32"`
	NamaRuang string  `json:"nama_ruang" validate:"required,max=255"`
}

type GetRuangRequest struct {
	ID int64 `param:"id" validate:"required,gt=0"`
}

type ListRuangRequest struct {
	PageRequest
	Search string `query:"search" validate:"omitempty,max=255"`
	// Nil lists every room; set it to filter on is_aktif.
	IsAktif *bool `query:"is_aktif"`
}
