package model

import "time"

// SaldoAwalResponse is one opening-stock document: the stock a unit_kerja already
// held on its shelves before its first row entered this system (isu #43).
//
// It has no supplier, no payable and no billed total — no counterparty at all. The
// only money figure is total_nilai, the value that entered inventory.
type SaldoAwalResponse struct {
	ID        int64     `json:"id"`
	Nomor     string    `json:"nomor"`
	Tanggal   time.Time `json:"tanggal"`
	IDRuang   int64     `json:"id_ruang"`
	NamaRuang string    `json:"nama_ruang,omitempty"`
	Alasan    string    `json:"alasan"`
	Status    string    `json:"status"`

	// Null until POSTED — unknown during every earlier state, not missing.
	TotalNilai *string `json:"total_nilai"`

	// Detail is filled on detail reads only; a list would need a query per row.
	Detail []SaldoAwalDetailResponse `json:"detail,omitempty"`

	CreatedBy     int64      `json:"created_by"`
	CreatedAt     time.Time  `json:"created_at"`
	DiajukanOleh  *int64     `json:"diajukan_oleh"`
	DiajukanPada  *time.Time `json:"diajukan_pada"`
	DisetujuiOleh *int64     `json:"disetujui_oleh"`
	DisetujuiPada *time.Time `json:"disetujui_pada"`
	PostedAt      *time.Time `json:"posted_at"`

	DibatalkanOleh *int64  `json:"dibatalkan_oleh"`
	AlasanBatal    *string `json:"alasan_batal"`
	AlasanTolak    *string `json:"alasan_tolak"`
}

// SaldoAwalDetailResponse is one line: a product, the quantity and the unit price the
// operator typed, and the value entering stock.
//
// nilai_masuk is qty_input × harga_satuan_input, rounded once. harga_pokok_satuan_dasar
// is only that price per base unit, for reading — the value never passes through it.
type SaldoAwalDetailResponse struct {
	ID          int64  `json:"id"`
	IDProduct   int64  `json:"id_product"`
	KodeBarang  string `json:"kode_barang,omitempty"`
	NamaProduct string `json:"nama_product,omitempty"`

	QtyInput        string `json:"qty_input"`
	IDSatuanInput   int64  `json:"id_satuan_input"`
	NamaSatuan      string `json:"nama_satuan,omitempty"`
	FaktorKonversi  int64  `json:"faktor_konversi"`
	QtyDasar        int64  `json:"qty_dasar"`
	NamaSatuanDasar string `json:"nama_satuan_dasar,omitempty"`

	HargaSatuanInput      string `json:"harga_satuan_input"`
	HargaPokokSatuanDasar string `json:"harga_pokok_satuan_dasar"`
	NilaiMasuk            string `json:"nilai_masuk"`

	// Null until the document is posted.
	IDKartuStok *int64 `json:"id_kartu_stok"`
}

// CreateSaldoAwalRequest opens a DRAFT with its lines. Lines are optional here and
// mandatory on ReplaceDetail, the asymmetry mutasi and pemakaian allow: submitting
// refuses a document with no lines anyway.
//
// The document is always created as DRAFT — status is not in this DTO.
type CreateSaldoAwalRequest struct {
	ActorID int64 `json:"-" validate:"required,gt=0"`

	// AktifIDUnitKerja is filled from the session's active grant by the controller,
	// never from the body — isu #12 fase 5. Nil means unrestricted.
	AktifIDUnitKerja *int64 `json:"-"`

	Tanggal string `json:"tanggal" validate:"required,datetime=2006-01-02"`
	IDRuang int64  `json:"id_ruang" validate:"required,gt=0"`
	Alasan  string `json:"alasan" validate:"required,max=1000"`

	Detail []SaldoAwalDetailRequest `json:"detail" validate:"omitempty,max=500,dive"`
}

// SaldoAwalDetailRequest is one line. The same product may NOT appear twice in one
// document: the quota here is "exactly one, for life", not the room's balance.
type SaldoAwalDetailRequest struct {
	IDProduct     int64  `json:"id_product" validate:"required,gt=0"`
	IDSatuanInput int64  `json:"id_satuan_input" validate:"required,gt=0"`
	QtyInput      string `json:"qty_input" validate:"required,numeric,max=23"`
	// HargaSatuanInput is the typed cost per input unit. Zero is refused on purpose —
	// see migration 000030.
	HargaSatuanInput string `json:"harga_satuan_input" validate:"required,numeric,max=23"`
}

type GetSaldoAwalRequest struct {
	ID int64 `param:"id" validate:"required,gt=0"`

	// AktifIDUnitKerja is filled by the controller, never from the request. Nil means
	// unrestricted; otherwise a document whose id_ruang falls outside this unit
	// answers 404.
	AktifIDUnitKerja *int64 `json:"-"`
}

// ListSaldoAwalRequest filters the document list.
type ListSaldoAwalRequest struct {
	PageRequest
	Search        string  `query:"search" validate:"omitempty,max=255"`
	Status        string  `query:"status" validate:"omitempty,oneof=DRAFT DIAJUKAN POSTED BATAL"`
	IDRuang       int64   `query:"id_ruang" validate:"omitempty,gt=0"`
	TanggalDari   *string `query:"tanggal_dari" validate:"omitempty,datetime=2006-01-02"`
	TanggalSampai *string `query:"tanggal_sampai" validate:"omitempty,datetime=2006-01-02"`

	// AktifIDUnitKerja, same rule as GetSaldoAwalRequest.
	AktifIDUnitKerja *int64 `query:"-"`
}

// UpdateSaldoAwalRequest patches the header of a DRAFT. Every field is a NOT NULL
// column, so an explicit null is rejected rather than silently ignored — and alasan in
// particular may be changed but never cleared: it is the only record of why inventory
// value was created without a document behind it.
type UpdateSaldoAwalRequest struct {
	ID      int64 `json:"-" validate:"required,gt=0"`
	ActorID int64 `json:"-" validate:"required,gt=0"`

	// AktifIDUnitKerja is only checked when id_ruang is actually being changed, and
	// only against the new value.
	AktifIDUnitKerja *int64 `json:"-"`

	Tanggal Optional[string] `json:"tanggal" validate:"omitempty,datetime=2006-01-02"`
	IDRuang Optional[int64]  `json:"id_ruang" validate:"omitempty,gt=0"`
	Alasan  Optional[string] `json:"alasan" validate:"omitempty,max=1000"`
}

// ReplaceSaldoAwalDetailRequest swaps the whole line set of a DRAFT — the shape used
// to type 500 SKUs at once, and the reason a bulk-import endpoint is out of scope.
type ReplaceSaldoAwalDetailRequest struct {
	ID      int64                    `json:"-" validate:"required,gt=0"`
	ActorID int64                    `json:"-" validate:"required,gt=0"`
	Detail  []SaldoAwalDetailRequest `json:"detail" validate:"required,min=1,max=500,dive"`
}

// AjukanSaldoAwalRequest hands a draft to the approver. No fields of its own.
type AjukanSaldoAwalRequest struct {
	ID      int64 `json:"-" validate:"required,gt=0"`
	ActorID int64 `json:"-" validate:"required,gt=0"`
}

// TolakSaldoAwalRequest sends a submission back to DRAFT. Alasan is required: a
// rejection with no reason gives the operator nothing to fix.
type TolakSaldoAwalRequest struct {
	ID      int64  `json:"-" validate:"required,gt=0"`
	ActorID int64  `json:"-" validate:"required,gt=0"`
	Alasan  string `json:"alasan" validate:"required,max=500"`
}

// PostingSaldoAwalRequest approves a submission and writes kartu_stok.
type PostingSaldoAwalRequest struct {
	ID      int64 `json:"-" validate:"required,gt=0"`
	ActorID int64 `json:"-" validate:"required,gt=0"`
}

// BatalSaldoAwalRequest voids a document. AlasanBatal is required: a reversal nobody
// explained is indistinguishable from a mistake, and kartu_stok keeps both forever.
type BatalSaldoAwalRequest struct {
	ID          int64  `json:"-" validate:"required,gt=0"`
	ActorID     int64  `json:"-" validate:"required,gt=0"`
	AlasanBatal string `json:"alasan_batal" validate:"required,max=500"`
}
