package model

import "time"

// MutasiResponse is one transfer between rooms.
//
// There is no total, no subtotal, and no money figure of any kind on the header, and
// that absence is the shape of the module rather than an omission. A mutasi bills
// nobody: it moves goods, and what they are worth is decided per line by the kartu_stok
// trigger at posting, from the source room's moving average.
//
// The one decimal figure that does appear, harga_pokok_satuan_dasar on the lines, is a
// string like every other NUMERIC in this API — a JSON float would round it, and it is
// part of the inventory valuation.
type MutasiResponse struct {
	ID      int64     `json:"id"`
	Nomor   string    `json:"nomor"`
	Tanggal time.Time `json:"tanggal"`

	IDRuangAsal     int64  `json:"id_ruang_asal"`
	NamaRuangAsal   string `json:"nama_ruang_asal,omitempty"`
	IDRuangTujuan   int64  `json:"id_ruang_tujuan"`
	NamaRuangTujuan string `json:"nama_ruang_tujuan,omitempty"`

	Keterangan *string `json:"keterangan"`
	Status     string  `json:"status"`

	// Detail is filled on detail reads only; a list would need a query per row.
	Detail []MutasiDetailResponse `json:"detail,omitempty"`

	CreatedBy int64      `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
	PostedAt  *time.Time `json:"posted_at"`

	DibatalkanOleh *int64  `json:"dibatalkan_oleh"`
	AlasanBatal    *string `json:"alasan_batal"`
}

// MutasiDetailResponse is one line of a transfer.
//
// It names a product directly, unlike penerimaan_susulan and retur_pembelian which name
// another document's line. A mutasi has no parent document — what limits it is the
// source room's balance, and GET /product/{id}/stok is how a screen sees that before
// anyone types.
type MutasiDetailResponse struct {
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

	// HargaPokokSatuanDasar is null until the document is posted, and is then the cost
	// the source room actually carried — copied from what the outgoing kartu_stok row
	// reported, never computed here. The application cannot know that number before the
	// insert: the moving average is read inside the trigger's advisory lock.
	HargaPokokSatuanDasar *string `json:"harga_pokok_satuan_dasar"`
}

// CreateMutasiRequest opens a DRAFT transfer.
//
// Both rooms are chosen, unlike retur_pembelian which copies them from its parent: a
// mutasi has no parent, and choosing where goods go is the entire content of the
// document.
//
// Lines are optional at create time. A transfer is often opened while the trolley is
// still being loaded, and posting refuses a document with no lines anyway.
type CreateMutasiRequest struct {
	ActorID int64 `json:"-" validate:"required,gt=0"`

	Tanggal       string `json:"tanggal" validate:"required,datetime=2006-01-02"`
	IDRuangAsal   int64  `json:"id_ruang_asal" validate:"required,gt=0"`
	IDRuangTujuan int64  `json:"id_ruang_tujuan" validate:"required,gt=0"`
	Keterangan    string `json:"keterangan" validate:"omitempty,max=1000"`

	Detail []MutasiDetailRequest `json:"detail" validate:"omitempty,max=500,dive"`
}

// MutasiDetailRequest is one line: what is moving and how much.
//
// id_satuan_input is asked for rather than assumed, and the factor behind it is
// resolved from product_satuan and stored as a snapshot — same as every other document.
// The same product may appear on more than one line, which is how 2 DUS and 3 PCS of
// one item get typed.
type MutasiDetailRequest struct {
	IDProduct     int64  `json:"id_product" validate:"required,gt=0"`
	IDSatuanInput int64  `json:"id_satuan_input" validate:"required,gt=0"`
	QtyInput      string `json:"qty_input" validate:"required,numeric,max=23"`
}

type GetMutasiRequest struct {
	ID int64 `param:"id" validate:"required,gt=0"`
}

// UpdateMutasiRequest patches the header of a DRAFT.
//
// Both rooms are present, unlike UpdatePembayaranUtangRequest which keeps id_supplier
// out. There, allocations point at one supplier's invoices and changing it would leave
// them pointing at another's. Here no detail row names a room — the rooms are on the
// header alone — so nothing is left behind pointing at the wrong place.
type UpdateMutasiRequest struct {
	ID      int64 `json:"-" validate:"required,gt=0"`
	ActorID int64 `json:"-" validate:"required,gt=0"`

	Tanggal       Optional[string] `json:"tanggal" validate:"omitempty,datetime=2006-01-02"`
	IDRuangAsal   Optional[int64]  `json:"id_ruang_asal" validate:"omitempty,gt=0"`
	IDRuangTujuan Optional[int64]  `json:"id_ruang_tujuan" validate:"omitempty,gt=0"`
	Keterangan    Optional[string] `json:"keterangan" validate:"omitempty,max=1000"`
}

// ReplaceMutasiDetailRequest swaps the whole line set of a DRAFT, for the same reason
// pembelian does: the lines are one thing, counted and loaded together, and a partial
// edit leaves a document disagreeing with itself between requests.
type ReplaceMutasiDetailRequest struct {
	ID      int64                 `json:"-" validate:"required,gt=0"`
	ActorID int64                 `json:"-" validate:"required,gt=0"`
	Detail  []MutasiDetailRequest `json:"detail" validate:"required,min=1,max=500,dive"`
}

type PostingMutasiRequest struct {
	ID      int64 `json:"-" validate:"required,gt=0"`
	ActorID int64 `json:"-" validate:"required,gt=0"`
}

type BatalMutasiRequest struct {
	ID          int64  `json:"-" validate:"required,gt=0"`
	ActorID     int64  `json:"-" validate:"required,gt=0"`
	AlasanBatal string `json:"alasan_batal" validate:"required,max=500"`
}

// ListMutasiRequest filters the document list.
//
// status must be filterable, and DRAFT is the case that matters: without a DIAJUKAN
// state there is no "this one is ready" signal, so the list of DRAFTs is the whole of
// SUPERADMIN's work queue. TerlamaDulu is what makes it read like one — oldest first,
// as GET /supplier/{id}/utang does, because a queue is read to be worked through.
type ListMutasiRequest struct {
	PageRequest
	Search        string  `query:"search" validate:"omitempty,max=255"`
	Status        string  `query:"status" validate:"omitempty,oneof=DRAFT POSTED BATAL"`
	IDRuangAsal   int64   `query:"id_ruang_asal" validate:"omitempty,gt=0"`
	IDRuangTujuan int64   `query:"id_ruang_tujuan" validate:"omitempty,gt=0"`
	TanggalDari   *string `query:"tanggal_dari" validate:"omitempty,datetime=2006-01-02"`
	TanggalSampai *string `query:"tanggal_sampai" validate:"omitempty,datetime=2006-01-02"`
	TerlamaDulu   bool    `query:"terlama_dulu"`
}
