package model

import "time"

// StokOpnameResponse is one physical count session against one ruang.
//
// While Status is DRAFT or DIAJUKAN, the ruang named by IDRuang is frozen: no
// other document may post into it — isu #15. NomorOpnameBeku on RuangResponse is
// the read-side mirror of this fact.
type StokOpnameResponse struct {
	ID        int64      `json:"id"`
	Nomor     string     `json:"nomor"`
	IDRuang   int64      `json:"id_ruang"`
	NamaRuang string     `json:"nama_ruang,omitempty"`
	TglBuka   time.Time  `json:"tgl_buka"`
	TglTutup  *time.Time `json:"tgl_tutup"`
	TsCutoff  time.Time  `json:"ts_cutoff"`
	UraianSO  *string    `json:"uraian_so"`
	Status    string     `json:"status"`

	// JumlahBaris and JumlahBelumDihitung ride on a detail read only — see
	// StokOpnameUseCase.detail. A partial count (one shelf, one category) is a
	// legitimate way to work, and this is how a verifier decides with open eyes
	// rather than being blocked by it or kept in the dark about it.
	JumlahBaris         int `json:"jumlah_baris,omitempty"`
	JumlahBelumDihitung int `json:"jumlah_belum_dihitung,omitempty"`

	// Detail is filled on detail reads only; a list would need a query per row.
	Detail []StokOpnameDetailResponse `json:"detail,omitempty"`

	CreatedBy int64     `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`

	VerifiedBy *int64     `json:"verified_by"`
	TsVerified *time.Time `json:"ts_verified"`
	PostedAt   *time.Time `json:"posted_at"`

	DibatalkanOleh *int64     `json:"dibatalkan_oleh"`
	AlasanBatal    *string    `json:"alasan_batal"`
	TsBatal        *time.Time `json:"ts_batal"`
}

// StokOpnameDetailResponse is one counted line.
//
// StokSO is nil for a line not yet counted — not zero. Reading it as zero would
// claim the shelf was checked and found empty, which is a different fact from
// "nobody has looked yet".
type StokOpnameDetailResponse struct {
	ID              int64  `json:"id"`
	IDProduct       int64  `json:"id_product"`
	KodeBarang      string `json:"kode_barang,omitempty"`
	NamaProduct     string `json:"nama_product,omitempty"`
	NamaSatuanDasar string `json:"nama_satuan_dasar,omitempty"`

	StokAwal int64  `json:"stok_awal"`
	StokSO   *int64 `json:"stok_so"`

	// Always recomputed from StokSO against StokAwal, never accepted from a
	// form — the same rule status_pembayaran follows.
	StokSelisihLebih  int64   `json:"stok_selisih_lebih"`
	StokSelisihKurang int64   `json:"stok_selisih_kurang"`
	Keterangan        *string `json:"keterangan"`

	// IDKartuStokPenyesuaian is null until posting, and stays null forever for a
	// line whose selisih came out zero.
	IDKartuStokPenyesuaian *int64 `json:"id_kartu_stok_penyesuaian"`
}

// CreateStokOpnameRequest opens a count session against one ruang. TsCutoff is
// never part of this DTO — it is stamped by the server from now(), and a client
// permitted to choose it would be a client permitted to choose the selisih.
type CreateStokOpnameRequest struct {
	ActorID int64 `json:"-" validate:"required,gt=0"`

	// AktifIDUnitKerja is filled from the session's active grant by the
	// controller, never from the body — isu #12 fase 5, the same mechanism
	// pembelian and mutasi already use.
	AktifIDUnitKerja *int64 `json:"-"`

	IDRuang  int64   `json:"id_ruang" validate:"required,gt=0"`
	UraianSO *string `json:"uraian_so" validate:"omitempty,max=1000"`
}

type GetStokOpnameRequest struct {
	ID int64 `param:"id" validate:"required,gt=0"`

	// AktifIDUnitKerja is filled from the session's active grant by the
	// controller, never from the request — isu #12 fase 6. Nil means the
	// active grant applies everywhere; otherwise a document outside this unit
	// answers 404.
	AktifIDUnitKerja *int64 `json:"-"`
}

// ListStokOpnameRequest filters the document list.
//
// status=DIAJUKAN with terlama_dulu=true is the verification queue, the same
// shape every other module's queue uses. Since the freeze exists, this list is
// also the list of rooms currently unable to post anything.
type ListStokOpnameRequest struct {
	PageRequest
	Search        string  `query:"search" validate:"omitempty,max=255"`
	Status        string  `query:"status" validate:"omitempty,oneof=DRAFT DIAJUKAN POSTED BATAL"`
	IDRuang       int64   `query:"id_ruang" validate:"omitempty,gt=0"`
	TanggalDari   *string `query:"tanggal_dari" validate:"omitempty,datetime=2006-01-02"`
	TanggalSampai *string `query:"tanggal_sampai" validate:"omitempty,datetime=2006-01-02"`
	TerlamaDulu   bool    `query:"terlama_dulu"`

	// AktifIDUnitKerja, same rule as GetStokOpnameRequest.
	AktifIDUnitKerja *int64 `query:"-"`
}

// UpdateStokOpnameRequest patches a DRAFT's uraian_so — the only header field a
// PATCH may touch. id_ruang cannot move: the freeze, the cutoff snapshot, and
// every line already point at the room the document was opened against.
type UpdateStokOpnameRequest struct {
	ID      int64 `json:"-" validate:"required,gt=0"`
	ActorID int64 `json:"-" validate:"required,gt=0"`

	UraianSO Optional[string] `json:"uraian_so" validate:"omitempty,max=1000"`
}

// TarikSaldoStokOpnameRequest pulls the room's balance at ts_cutoff into the
// document's lines. It carries no fields of its own — everything it needs is
// already on the header.
type TarikSaldoStokOpnameRequest struct {
	ID      int64 `json:"-" validate:"required,gt=0"`
	ActorID int64 `json:"-" validate:"required,gt=0"`
}

// StokOpnameDetailRequest names one product to count. Unlike every document that
// converts a typed quantity into base units, there is no satuan and no qty here:
// the count itself is always in the base unit, because it is compared directly
// against kartu_stok's own running balance.
type StokOpnameDetailRequest struct {
	IDProduct int64 `json:"id_product" validate:"required,gt=0"`
}

// ReplaceStokOpnameDetailRequest swaps the whole line set of a DRAFT — this is
// how a line TarikSaldo's automatic pull missed gets added by hand, following the
// same "pasangan (barang, ruang) must already have a kartu_stok row" rule
// TarikSaldo itself is bound by.
type ReplaceStokOpnameDetailRequest struct {
	ID      int64                      `json:"-" validate:"required,gt=0"`
	ActorID int64                      `json:"-" validate:"required,gt=0"`
	Detail  []StokOpnameDetailRequest  `json:"detail" validate:"required,min=1,max=1000,dive"`
}

// UpdateStokOpnameDetailRequest fills in one line's physical count and/or note —
// the deliberate exception to "lines are replaced wholesale, never edited one at
// a time" that every other document in this API follows. A count sheet is filled
// in by someone walking the room product by product, and requiring the whole
// list to be retyped every time one line finishes would lose the count every time
// the network drops.
type UpdateStokOpnameDetailRequest struct {
	ID       int64 `json:"-" validate:"required,gt=0"`
	IDDetail int64 `json:"-" validate:"required,gt=0"`
	ActorID  int64 `json:"-" validate:"required,gt=0"`

	StokSO     Optional[int64]  `json:"stok_so" validate:"omitempty,gte=0"`
	Keterangan Optional[string] `json:"keterangan" validate:"omitempty,max=500"`
}

type AjukanStokOpnameRequest struct {
	ID      int64 `json:"-" validate:"required,gt=0"`
	ActorID int64 `json:"-" validate:"required,gt=0"`
}

// TolakStokOpnameRequest sends a submitted count back to DRAFT. It carries no
// reason field: the schema has no column to hold one for this document, unlike
// pemakaian's catatan_persetujuan — see StokOpnameRepository.Tolak.
type TolakStokOpnameRequest struct {
	ID      int64 `json:"-" validate:"required,gt=0"`
	ActorID int64 `json:"-" validate:"required,gt=0"`
}

type PostingStokOpnameRequest struct {
	ID      int64 `json:"-" validate:"required,gt=0"`
	ActorID int64 `json:"-" validate:"required,gt=0"`
}

// BatalStokOpnameRequest voids a document from any status. AlasanBatal is
// required, the same rule every other reversible document in this API follows —
// a cancellation nobody explained is indistinguishable from a mistake.
type BatalStokOpnameRequest struct {
	ID          int64  `json:"-" validate:"required,gt=0"`
	ActorID     int64  `json:"-" validate:"required,gt=0"`
	AlasanBatal string `json:"alasan_batal" validate:"required,max=500"`
}
