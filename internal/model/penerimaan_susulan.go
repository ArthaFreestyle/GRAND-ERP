package model

import "time"

// PenerimaanSusulanResponse is one follow-up receipt: the second shipment for goods
// that did not arrive with the first.
//
// It has no total, no PPN, and no payment status, because it is not a bill. The
// supplier's invoice was issued in full with the first delivery and booked in full
// then; what is outstanding after that is goods, not money. total_nilai is the
// value of what turned up late, carried into inventory.
type PenerimaanSusulanResponse struct {
	ID             int64     `json:"id"`
	Nomor          string    `json:"nomor"`
	Tanggal        time.Time `json:"tanggal"`
	IDPembelian    int64     `json:"id_pembelian"`
	NomorPembelian string    `json:"nomor_pembelian,omitempty"`
	IDSupplier     int64     `json:"id_supplier"`
	NamaSupplier   string    `json:"nama_supplier,omitempty"`
	IDRuang        int64     `json:"id_ruang"`
	NamaRuang      string    `json:"nama_ruang,omitempty"`
	Keterangan     *string   `json:"keterangan"`
	TotalNilai     string    `json:"total_nilai"`
	Status         string    `json:"status"`

	// Detail is filled on detail reads only; a list would need a query per row.
	Detail []PenerimaanSusulanDetailResponse `json:"detail,omitempty"`

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

// PenerimaanSusulanDetailResponse is one line, naming the invoice line it completes
// rather than a product directly — the product is whatever that line was for.
type PenerimaanSusulanDetailResponse struct {
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

	// HargaPokokSatuanDasar is copied from the source pembelian_detail, never
	// recomputed and never read off the current moving average: what the goods cost
	// was settled by the invoice.
	HargaPokokSatuanDasar string `json:"harga_pokok_satuan_dasar"`
	Nilai                 string `json:"nilai"`
}

// CreatePenerimaanSusulanRequest opens a DRAFT follow-up receipt against one
// purchase.
//
// id_supplier and id_ruang are absent: both are copied from the purchase. The
// supplier is fixed by the document this is a remainder of, and the room was
// already settled there — goods that need to move rooms after arrival are a mutasi.
//
// The purchase must be POSTED. Before that, its lines carry no
// harga_pokok_satuan_dasar to copy and no settled outstanding quantity to check
// against.
type CreatePenerimaanSusulanRequest struct {
	ActorID int64 `json:"-" validate:"required,gt=0"`

	IDPembelian int64   `json:"id_pembelian" validate:"required,gt=0"`
	Tanggal     string  `json:"tanggal" validate:"required,datetime=2006-01-02"`
	Keterangan  *string `json:"keterangan" validate:"omitempty,max=1000"`

	Detail []PenerimaanSusulanDetailRequest `json:"detail" validate:"required,min=1,max=500,dive"`
}

// PenerimaanSusulanDetailRequest is one line of a follow-up receipt.
//
// It names a pembelian_detail rather than a product: this is the remainder of a
// specific invoice line, and that line is what fixes both the outstanding quantity
// and the cost to carry.
//
// id_satuan_input is asked for rather than inherited from the source line, because
// the quantity is a fresh count and may well arrive in a different unit than the
// invoice used — five loose pieces short of a line typed in boxes. The factor is
// resolved from product_satuan and stored as a snapshot, exactly as pembelian does.
type PenerimaanSusulanDetailRequest struct {
	IDPembelianDetail int64  `json:"id_pembelian_detail" validate:"required,gt=0"`
	IDSatuanInput     int64  `json:"id_satuan_input" validate:"required,gt=0"`
	QtyInput          string `json:"qty_input" validate:"required,numeric,max=23"`
}

type GetPenerimaanSusulanRequest struct {
	ID int64 `param:"id" validate:"required,gt=0"`
}

// UpdatePenerimaanSusulanRequest patches the header of a DRAFT. Only the two fields
// that are genuinely the operator's to change appear.
type UpdatePenerimaanSusulanRequest struct {
	ID      int64 `json:"-" validate:"required,gt=0"`
	ActorID int64 `json:"-" validate:"required,gt=0"`

	Tanggal    Optional[string] `json:"tanggal" validate:"omitempty,datetime=2006-01-02"`
	Keterangan Optional[string] `json:"keterangan" validate:"omitempty,max=1000"`
}

// ReplacePenerimaanSusulanDetailRequest swaps the whole line set of a DRAFT, for
// the same reason pembelian does: the lines of one shipment are one thing, counted
// together.
type ReplacePenerimaanSusulanDetailRequest struct {
	ID      int64                            `json:"-" validate:"required,gt=0"`
	ActorID int64                            `json:"-" validate:"required,gt=0"`
	Detail  []PenerimaanSusulanDetailRequest `json:"detail" validate:"required,min=1,max=500,dive"`
}

type AjukanPenerimaanSusulanRequest struct {
	ID      int64 `json:"-" validate:"required,gt=0"`
	ActorID int64 `json:"-" validate:"required,gt=0"`
}

type TolakPenerimaanSusulanRequest struct {
	ID      int64  `json:"-" validate:"required,gt=0"`
	ActorID int64  `json:"-" validate:"required,gt=0"`
	Alasan  string `json:"alasan" validate:"required,max=500"`
}

type PostingPenerimaanSusulanRequest struct {
	ID      int64 `json:"-" validate:"required,gt=0"`
	ActorID int64 `json:"-" validate:"required,gt=0"`
}

type BatalPenerimaanSusulanRequest struct {
	ID          int64  `json:"-" validate:"required,gt=0"`
	ActorID     int64  `json:"-" validate:"required,gt=0"`
	AlasanBatal string `json:"alasan_batal" validate:"required,max=500"`
}

type ListPenerimaanSusulanRequest struct {
	PageRequest
	Search        string  `query:"search" validate:"omitempty,max=255"`
	Status        string  `query:"status" validate:"omitempty,oneof=DRAFT DIAJUKAN POSTED BATAL"`
	IDPembelian   int64   `query:"id_pembelian" validate:"omitempty,gt=0"`
	TanggalDari   *string `query:"tanggal_dari" validate:"omitempty,datetime=2006-01-02"`
	TanggalSampai *string `query:"tanggal_sampai" validate:"omitempty,datetime=2006-01-02"`
}
