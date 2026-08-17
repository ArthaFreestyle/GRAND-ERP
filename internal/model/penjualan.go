package model

import "time"

// PenjualanResponse is one sales nota — the sixth document to write kartu_stok and
// the first to take goods out to an outside party with money moving on the other
// side of it.
//
// total_hpp is null until POSTED, the same rule pemakaian's total_hpp follows: it is
// filled from what the outgoing kartu_stok rows actually reported, never computed
// from a moving average read beforehand. Once it is there, margin is free —
// total - total_hpp, no new table and no recomputation — which is the only reason
// the column exists at all.
type PenjualanResponse struct {
	ID      int64     `json:"id"`
	Nomor   string    `json:"nomor"`
	Tanggal time.Time `json:"tanggal"`

	IDRuang   int64  `json:"id_ruang"`
	NamaRuang string `json:"nama_ruang,omitempty"`

	// IDPelanggan is nil for a cash sale at the counter — no customer needs to be
	// on file to pay TUNAI. penjualan_kredit_pelanggan_check is what forbids the
	// one combination that must not happen: KREDIT with no pelanggan.
	IDPelanggan   *int64  `json:"id_pelanggan"`
	NamaPelanggan *string `json:"nama_pelanggan,omitempty"`

	Subtotal   string  `json:"subtotal"`
	DiskonNota string  `json:"diskon_nota"`
	Pembulatan string  `json:"pembulatan"`
	Total      string  `json:"total"`
	TotalHPP   *string `json:"total_hpp"`

	JenisPembayaran string `json:"jenis_pembayaran"`
	// StatusPembayaran is a cache, always recomputed and never set from a form —
	// same rule as pembelian's. A TUNAI nota that POSTED reads LUNAS immediately;
	// see PenjualanRepository.RecalculateStatusPembayaran for why that is not a
	// special case bolted on afterwards.
	StatusPembayaran string `json:"status_pembayaran"`
	Status           string `json:"status"`

	// Detail is filled on detail reads only; a list would need a query per row.
	Detail []PenjualanDetailResponse `json:"detail,omitempty"`

	CreatedBy int64      `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
	PostedAt  *time.Time `json:"posted_at"`

	DibatalkanOleh *int64  `json:"dibatalkan_oleh"`
	AlasanBatal    *string `json:"alasan_batal"`
}

// PenjualanDetailResponse is one line: a product, what was billed, and — once
// posted — what it cost.
//
// harga_satuan_input is the snapshot that is actually billed, and it is never forced
// to equal product_harga_jual — bargaining happens at the counter, and the nota is
// what is true. id_harga_jual only records which price-list version, if any,
// proposed that figure.
type PenjualanDetailResponse struct {
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

	IDHargaJual      *int64 `json:"id_harga_jual"`
	HargaSatuanInput string `json:"harga_satuan_input"`
	DiskonBaris      string `json:"diskon_baris"`
	Subtotal         string `json:"subtotal"`

	// Both null until the document is posted — unknown during DRAFT, not missing.
	HPPSatuanDasar *string `json:"hpp_satuan_dasar"`
	HPPTotal       *string `json:"hpp_total"`
}

// CreatePenjualanRequest opens a DRAFT with its lines.
//
// The document is always created DRAFT — status is not in this DTO at all. Stock
// moves only at posting, a separate, differently-shaped call the same cashier may
// still make in one motion at the counter.
//
// Lines are optional here and mandatory on ReplaceDetail, the same asymmetry
// mutasi and pemakaian allow: a nota is commonly typed while items are still being
// scanned, and posting refuses a document with no lines anyway.
type CreatePenjualanRequest struct {
	ActorID int64 `json:"-" validate:"required,gt=0"`

	// AktifIDUnitKerja is filled from the session's active grant by the
	// controller, never from the body — isu #21 fase 2. Nil means the active
	// grant applies everywhere, so nothing is checked.
	AktifIDUnitKerja *int64 `json:"-"`

	Tanggal         string `json:"tanggal" validate:"required,datetime=2006-01-02"`
	IDRuang         int64  `json:"id_ruang" validate:"required,gt=0"`
	IDPelanggan     *int64 `json:"id_pelanggan" validate:"omitempty,gt=0"`
	JenisPembayaran string `json:"jenis_pembayaran" validate:"omitempty,oneof=TUNAI KREDIT"`

	// Money arrives as decimal strings. Empty means zero. Pembulatan may be
	// negative — it is the nota's rounding line, either way, and this project
	// leaves it typed by the cashier rather than computed to a configured
	// multiple: the latter would need a new config key for no benefit this issue
	// asks for.
	DiskonNota string `json:"diskon_nota" validate:"omitempty,numeric,max=21"`
	Pembulatan string `json:"pembulatan" validate:"omitempty,numeric,max=21"`

	Detail []PenjualanDetailRequest `json:"detail" validate:"omitempty,max=500,dive"`
}

// PenjualanDetailRequest is one line as the cashier types it.
//
// harga_satuan_input is required and is what is actually charged; it is never
// forced to match product_harga_jual. id_harga_jual is optional — a product with no
// price yet may still be sold at a typed price — and when supplied is validated in
// the usecase against this line's own product, satuan, and the document's date.
type PenjualanDetailRequest struct {
	IDProduct     int64  `json:"id_product" validate:"required,gt=0"`
	IDSatuanInput int64  `json:"id_satuan_input" validate:"required,gt=0"`
	QtyInput      string `json:"qty_input" validate:"required,numeric,max=23"`

	IDHargaJual      *int64 `json:"id_harga_jual" validate:"omitempty,gt=0"`
	HargaSatuanInput string `json:"harga_satuan_input" validate:"required,numeric,max=21"`
	DiskonBaris      string `json:"diskon_baris" validate:"omitempty,numeric,max=21"`
}

type GetPenjualanRequest struct {
	ID int64 `param:"id" validate:"required,gt=0"`

	// AktifIDUnitKerja is filled from the session's active grant by the
	// controller, never from the request — isu #21 fase 2. Nil means
	// unrestricted, otherwise a document whose id_ruang falls outside this
	// unit answers 404.
	AktifIDUnitKerja *int64 `json:"-"`
}

// UpdatePenjualanRequest patches the header of a DRAFT.
//
// id_ruang, id_pelanggan, and jenis_pembayaran may all move, unlike
// UpdatePembayaranUtangRequest which keeps id_supplier out: no detail row here names
// any of the three, so changing one leaves nothing behind pointing at the wrong
// place. nomor, total_hpp, and status_pembayaran never appear — the first is
// identity, the other two are always derived.
//
// id_pelanggan is Optional[int64], not a bare pointer: an explicit null is a real
// request here — it clears the customer on a nota that is also being switched to
// TUNAI in the same patch. id_ruang and jenis_pembayaran are NOT NULL columns, so
// an explicit null for either is rejected in the usecase.
type UpdatePenjualanRequest struct {
	ID      int64 `json:"-" validate:"required,gt=0"`
	ActorID int64 `json:"-" validate:"required,gt=0"`

	// AktifIDUnitKerja, same rule as CreatePenjualanRequest: only checked when
	// id_ruang is actually being changed, and only against the new value.
	AktifIDUnitKerja *int64 `json:"-"`

	Tanggal         Optional[string] `json:"tanggal" validate:"omitempty,datetime=2006-01-02"`
	IDRuang         Optional[int64]  `json:"id_ruang" validate:"omitempty,gt=0"`
	IDPelanggan     Optional[int64]  `json:"id_pelanggan" validate:"omitempty,gt=0"`
	JenisPembayaran Optional[string] `json:"jenis_pembayaran" validate:"omitempty,oneof=TUNAI KREDIT"`
	DiskonNota      Optional[string] `json:"diskon_nota" validate:"omitempty,numeric,max=21"`
	Pembulatan      Optional[string] `json:"pembulatan" validate:"omitempty,numeric,max=21"`
}

// ReplacePenjualanDetailRequest swaps the whole line set of a DRAFT, for the same
// reason every other document does: the lines are one thing, scanned and totalled
// together, and a partial edit leaves the nota disagreeing with itself between
// requests.
type ReplacePenjualanDetailRequest struct {
	ID      int64                    `json:"-" validate:"required,gt=0"`
	ActorID int64                    `json:"-" validate:"required,gt=0"`
	Detail  []PenjualanDetailRequest `json:"detail" validate:"required,min=1,max=500,dive"`
}

type PostingPenjualanRequest struct {
	ID      int64 `json:"-" validate:"required,gt=0"`
	ActorID int64 `json:"-" validate:"required,gt=0"`
}

// BatalPenjualanRequest voids a posted nota by writing reversing kartu_stok rows.
// AlasanBatal is required, same rule every reversal in this codebase follows: a
// reversal nobody explained is indistinguishable from a mistake, and kartu_stok
// keeps both forever.
type BatalPenjualanRequest struct {
	ID          int64  `json:"-" validate:"required,gt=0"`
	ActorID     int64  `json:"-" validate:"required,gt=0"`
	AlasanBatal string `json:"alasan_batal" validate:"required,max=500"`
}

// ListPenjualanRequest filters the document list.
type ListPenjualanRequest struct {
	PageRequest
	Search           string  `query:"search" validate:"omitempty,max=255"`
	Status           string  `query:"status" validate:"omitempty,oneof=DRAFT POSTED BATAL"`
	StatusPembayaran string  `query:"status_pembayaran" validate:"omitempty,oneof=BELUM SEBAGIAN LUNAS"`
	JenisPembayaran  string  `query:"jenis_pembayaran" validate:"omitempty,oneof=TUNAI KREDIT"`
	IDRuang          int64   `query:"id_ruang" validate:"omitempty,gt=0"`
	IDPelanggan      int64   `query:"id_pelanggan" validate:"omitempty,gt=0"`
	TanggalDari      *string `query:"tanggal_dari" validate:"omitempty,datetime=2006-01-02"`
	TanggalSampai    *string `query:"tanggal_sampai" validate:"omitempty,datetime=2006-01-02"`

	// AktifIDUnitKerja, same rule as GetPenjualanRequest — isu #21 fase 2.
	AktifIDUnitKerja *int64 `query:"-"`
}

// PiutangPelangganResponse is one open KREDIT nota: the working list for chasing
// payment, the mirror of UtangSupplierResponse on the receivable side — isu #10
// fase 2.
//
// There is no jumlah_dialokasikan or nilai_kredit_retur here the way
// UtangSupplierResponse carries them: retur_penjualan and penerimaan_pembayaran are
// both out of scope for this issue, so sisa_piutang is simply total for now. The day
// either is built, this response gains the same figures and sisa_piutang stops being
// the whole answer — the shape does not have to change, only what feeds it.
type PiutangPelangganResponse struct {
	IDPenjualan int64     `json:"id_penjualan"`
	Nomor       string    `json:"nomor"`
	Tanggal     time.Time `json:"tanggal"`
	Total       string    `json:"total"`
	SisaPiutang string    `json:"sisa_piutang"`
}

// ListPiutangPelangganRequest lists a customer's open KREDIT notas, oldest first —
// the same choice GET /supplier/{id}/utang makes, because this is a queue to work
// through rather than a history to read.
type ListPiutangPelangganRequest struct {
	PageRequest
	IDPelanggan int64 `param:"id" validate:"required,gt=0"`
}
