package model

import "time"

// ReturPembelianResponse is one purchase return: goods sent back to the supplier.
//
// It is the mirror of PenerimaanSusulanResponse — same shape, goods moving the other
// way — and like it, it carries no PPN and no payment status. What the supplier
// credits back is settled with them on paper; this document records what left the
// warehouse and what that was worth.
//
// Every money and quantity figure is a decimal string. NUMERIC through a JSON float
// loses cents, and these become an inventory valuation.
type ReturPembelianResponse struct {
	ID             int64     `json:"id"`
	Nomor          string    `json:"nomor"`
	Tanggal        time.Time `json:"tanggal"`
	IDPembelian    int64     `json:"id_pembelian"`
	NomorPembelian string    `json:"nomor_pembelian,omitempty"`
	IDSupplier     int64     `json:"id_supplier"`
	NamaSupplier   string    `json:"nama_supplier,omitempty"`
	IDRuang        int64     `json:"id_ruang"`
	NamaRuang      string    `json:"nama_ruang,omitempty"`
	Alasan         *string   `json:"alasan"`
	// Total is the inventory value withdrawn, at the cost the invoice settled.
	// Recomputed from the lines, never set from a form. It is not automatically the
	// credit the supplier owes — see harga_pokok_satuan_dasar on the lines.
	Total string `json:"total"`
	// NilaiKreditUtang is what the supplier is credited, and it is deliberately a
	// different figure from total. total is the inventory value at cost, and cost carries
	// the freight share paid to the carrier — money the supplier never received. This is
	// the invoice value of the returned goods scaled to the purchase's total, so the nota
	// discount, the PPN, and the rounding line come with it.
	//
	// Zero until the document is posted. It is what pembelian.sisa_utang subtracts.
	NilaiKreditUtang string `json:"nilai_kredit_utang"`
	Status           string `json:"status"`

	// Detail is filled on detail reads only; a list would need a query per row.
	Detail []ReturPembelianDetailResponse `json:"detail,omitempty"`

	CreatedBy int64     `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`

	DiajukanOleh  *int64     `json:"diajukan_oleh"`
	DiajukanPada  *time.Time `json:"diajukan_pada"`
	DisetujuiOleh *int64     `json:"disetujui_oleh"`
	DisetujuiPada *time.Time `json:"disetujui_pada"`
	PostedAt      *time.Time `json:"posted_at"`

	DibatalkanOleh *int64  `json:"dibatalkan_oleh"`
	AlasanBatal    *string `json:"alasan_batal"`
	AlasanTolak    *string `json:"alasan_tolak"`
}

// ReturPembelianDetailResponse is one line, naming the invoice line the goods came in
// on rather than a product directly — the product is whatever that line was for.
type ReturPembelianDetailResponse struct {
	ID                int64  `json:"id"`
	IDPembelianDetail int64  `json:"id_pembelian_detail"`
	IDProduct         int64  `json:"id_product"`
	KodeBarang        string `json:"kode_barang,omitempty"`
	NamaProduct       string `json:"nama_product,omitempty"`

	QtyInput        string `json:"qty_input"`
	IDSatuanInput   int64  `json:"id_satuan_input"`
	NamaSatuan      string `json:"nama_satuan,omitempty"`
	FaktorKonversi  int64  `json:"faktor_konversi"`
	QtyDasar        int64  `json:"qty_dasar"`
	NamaSatuanDasar string `json:"nama_satuan_dasar,omitempty"`

	// HargaPokokSatuanDasar is copied from the source pembelian_detail, never read off
	// the current moving average: otherwise a purchase and its return would not cancel
	// out.
	HargaPokokSatuanDasar string `json:"harga_pokok_satuan_dasar"`
	// Nilai = qty_dasar x harga_pokok_satuan_dasar. This is what the goods were worth
	// on the invoice, not what kartu_stok records as leaving — the stock ledger values
	// every outgoing row at the moving average in force at the time.
	Nilai string `json:"nilai"`
}

// CreateReturPembelianRequest opens a DRAFT return against one purchase.
//
// id_supplier and id_ruang are absent: both are copied from the purchase. The
// supplier is whoever sold the goods, and the room is where the purchase put them.
//
// The purchase must be POSTED. Before that its lines carry no
// harga_pokok_satuan_dasar to copy, and nothing has physically arrived to send back.
type CreateReturPembelianRequest struct {
	ActorID int64 `json:"-" validate:"required,gt=0"`

	IDPembelian int64  `json:"id_pembelian" validate:"required,gt=0"`
	Tanggal     string `json:"tanggal" validate:"required,datetime=2006-01-02"`
	// Alasan is required even though the column is nullable. It is the only record of
	// why goods already paid for went back, and it is what gets read out to the
	// supplier — a policy, not a constraint.
	Alasan string `json:"alasan" validate:"required,max=1000"`

	Detail []ReturPembelianDetailRequest `json:"detail" validate:"required,min=1,max=500,dive"`
}

// ReturPembelianDetailRequest is one line of a return.
//
// It names a pembelian_detail rather than a product: these are goods from a specific
// invoice line, and that line is what fixes both how many may go back and what they
// cost.
//
// id_satuan_input is asked for rather than inherited from the source line, because
// the quantity is a fresh count of what is being packed and may well be in a
// different unit than the invoice used. The factor is resolved from product_satuan
// and stored as a snapshot, exactly as pembelian and penerimaan_susulan do.
type ReturPembelianDetailRequest struct {
	IDPembelianDetail int64  `json:"id_pembelian_detail" validate:"required,gt=0"`
	IDSatuanInput     int64  `json:"id_satuan_input" validate:"required,gt=0"`
	QtyInput          string `json:"qty_input" validate:"required,numeric,max=23"`
}

type GetReturPembelianRequest struct {
	ID int64 `param:"id" validate:"required,gt=0"`

	// AktifIDUnitKerja is filled from the session's active grant by the
	// controller, never from the request — isu #12 fase 6. Nil means
	// unrestricted; otherwise a document outside this unit answers 404.
	AktifIDUnitKerja *int64 `json:"-"`
}

// UpdateReturPembelianRequest patches the header of a DRAFT. Only the two fields that
// are genuinely the operator's to change appear.
type UpdateReturPembelianRequest struct {
	ID      int64 `json:"-" validate:"required,gt=0"`
	ActorID int64 `json:"-" validate:"required,gt=0"`

	Tanggal Optional[string] `json:"tanggal" validate:"omitempty,datetime=2006-01-02"`
	Alasan  Optional[string] `json:"alasan" validate:"omitempty,max=1000"`
}

// ReplaceReturPembelianDetailRequest swaps the whole line set of a DRAFT, for the
// same reason pembelian does: the lines of one shipment back are one thing, packed
// and counted together.
type ReplaceReturPembelianDetailRequest struct {
	ID      int64                         `json:"-" validate:"required,gt=0"`
	ActorID int64                         `json:"-" validate:"required,gt=0"`
	Detail  []ReturPembelianDetailRequest `json:"detail" validate:"required,min=1,max=500,dive"`
}

type AjukanReturPembelianRequest struct {
	ID      int64 `json:"-" validate:"required,gt=0"`
	ActorID int64 `json:"-" validate:"required,gt=0"`
}

type TolakReturPembelianRequest struct {
	ID      int64  `json:"-" validate:"required,gt=0"`
	ActorID int64  `json:"-" validate:"required,gt=0"`
	Alasan  string `json:"alasan" validate:"required,max=500"`
}

type PostingReturPembelianRequest struct {
	ID      int64 `json:"-" validate:"required,gt=0"`
	ActorID int64 `json:"-" validate:"required,gt=0"`
}

type BatalReturPembelianRequest struct {
	ID          int64  `json:"-" validate:"required,gt=0"`
	ActorID     int64  `json:"-" validate:"required,gt=0"`
	AlasanBatal string `json:"alasan_batal" validate:"required,max=500"`
}

type ListReturPembelianRequest struct {
	PageRequest
	Search        string  `query:"search" validate:"omitempty,max=255"`
	Status        string  `query:"status" validate:"omitempty,oneof=DRAFT DIAJUKAN POSTED BATAL"`
	IDPembelian   int64   `query:"id_pembelian" validate:"omitempty,gt=0"`
	IDSupplier    int64   `query:"id_supplier" validate:"omitempty,gt=0"`
	TanggalDari   *string `query:"tanggal_dari" validate:"omitempty,datetime=2006-01-02"`
	TanggalSampai *string `query:"tanggal_sampai" validate:"omitempty,datetime=2006-01-02"`

	// AktifIDUnitKerja, same rule as GetReturPembelianRequest.
	AktifIDUnitKerja *int64 `query:"-"`
}
