package model

import "time"

// PembayaranUtangResponse is one payment to a supplier.
//
// Every money figure is a decimal string, not a JSON number: NUMERIC(20,2) through a
// float loses cents, and these are the amounts a bank reconciliation is done against.
//
// jumlah_dialokasikan may be less than jumlah, and that is normal rather than an error:
// the remainder sits as a credit with the supplier for a later invoice.
type PembayaranUtangResponse struct {
	ID           int64     `json:"id"`
	Nomor        string    `json:"nomor"`
	Tanggal      time.Time `json:"tanggal"`
	IDSupplier   int64     `json:"id_supplier"`
	NamaSupplier string    `json:"nama_supplier,omitempty"`

	Metode      string  `json:"metode"`
	NoReferensi *string `json:"no_referensi"`
	NamaBank    *string `json:"nama_bank"`
	// Giro fields, all null unless metode is GIRO.
	TanggalJatuhTempoGiro *string `json:"tanggal_jatuh_tempo_giro"`
	TanggalCair           *string `json:"tanggal_cair"`
	// StatusGiro drives whether this payment reduces anything at all: an uncashed giro
	// is not a payment. BELUM_CAIR and TOLAK leave every payable untouched.
	StatusGiro *string `json:"status_giro"`

	Jumlah string `json:"jumlah"`
	// JumlahDialokasikan is recomputed from the allocation rows, never set from a form.
	JumlahDialokasikan string `json:"jumlah_dialokasikan"`
	// SisaBelumDialokasikan = jumlah - jumlah_dialokasikan: money paid that is not yet
	// pointed at an invoice. Reported rather than left to the client to subtract,
	// because it is the figure that says whether this payment still has room in it.
	SisaBelumDialokasikan string  `json:"sisa_belum_dialokasikan"`
	Status                string  `json:"status"`
	Keterangan            *string `json:"keterangan"`

	// Alokasi is filled on detail reads only; a list would need a query per row.
	Alokasi []PembayaranUtangAlokasiResponse `json:"alokasi,omitempty"`

	CreatedBy int64      `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
	PostedAt  *time.Time `json:"posted_at"`

	DibatalkanOleh *int64  `json:"dibatalkan_oleh"`
	AlasanBatal    *string `json:"alasan_batal"`
}

// PembayaranUtangAlokasiResponse is one allocation, naming the invoice it settles.
type PembayaranUtangAlokasiResponse struct {
	ID               int64   `json:"id"`
	IDPembelian      int64   `json:"id_pembelian"`
	NomorPembelian   string  `json:"nomor_pembelian,omitempty"`
	NoFakturSupplier *string `json:"no_faktur_supplier"`
	// TotalPembelian and StatusPembayaran describe the invoice as it stands now, so a
	// payment screen can show what it settled and what became of it.
	TotalPembelian   string    `json:"total_pembelian,omitempty"`
	StatusPembayaran string    `json:"status_pembayaran,omitempty"`
	Jumlah           string    `json:"jumlah"`
	CreatedAt        time.Time `json:"created_at"`
}

// CreatePembayaranUtangRequest opens a DRAFT payment with its allocations.
//
// The document is always created as DRAFT whatever the caller says — status is not in
// this DTO at all. Nothing is settled until posting, and posting is a separate,
// differently authorized call.
//
// alokasi may be omitted or empty: money can legitimately be paid to a supplier before
// it is decided which invoices it covers, and the remainder then sits as a credit. That
// is the same rule as jumlah_dialokasikan being allowed below jumlah.
type CreatePembayaranUtangRequest struct {
	ActorID int64 `json:"-" validate:"required,gt=0"`

	IDSupplier int64  `json:"id_supplier" validate:"required,gt=0"`
	Tanggal    string `json:"tanggal" validate:"required,datetime=2006-01-02"`

	Metode      string  `json:"metode" validate:"required,oneof=TUNAI TRANSFER GIRO"`
	NoReferensi *string `json:"no_referensi" validate:"omitempty,max=64"`
	NamaBank    *string `json:"nama_bank" validate:"omitempty,max=128"`
	// TanggalJatuhTempoGiro is only accepted for metode GIRO, and rejected otherwise —
	// a due date on a cash payment describes nothing.
	TanggalJatuhTempoGiro *string `json:"tanggal_jatuh_tempo_giro" validate:"omitempty,datetime=2006-01-02"`

	Jumlah     string  `json:"jumlah" validate:"required,numeric,max=21"`
	Keterangan *string `json:"keterangan" validate:"omitempty,max=1000"`

	Alokasi []PembayaranUtangAlokasiRequest `json:"alokasi" validate:"omitempty,max=500,dive"`
}

// PembayaranUtangAlokasiRequest points part of a payment at one invoice.
//
// The invoice must be POSTED and must belong to the payment's supplier. A DRAFT invoice
// is a typed page rather than a debt, and a BATAL one is a debt that was withdrawn —
// neither can be paid.
type PembayaranUtangAlokasiRequest struct {
	IDPembelian int64  `json:"id_pembelian" validate:"required,gt=0"`
	Jumlah      string `json:"jumlah" validate:"required,numeric,max=21"`
}

type GetPembayaranUtangRequest struct {
	ID int64 `param:"id" validate:"required,gt=0"`
}

// UpdatePembayaranUtangRequest patches the header of a DRAFT.
//
// id_supplier is absent: it is who the money goes to, and changing it would leave every
// allocation pointing at another supplier's invoices. metode is absent too, because it
// decides whether the giro columns may be filled at all — switch it by cancelling and
// re-entering, which is also what the bank statement will show.
type UpdatePembayaranUtangRequest struct {
	ID      int64 `json:"-" validate:"required,gt=0"`
	ActorID int64 `json:"-" validate:"required,gt=0"`

	Tanggal               Optional[string] `json:"tanggal" validate:"omitempty,datetime=2006-01-02"`
	NoReferensi           Optional[string] `json:"no_referensi" validate:"omitempty,max=64"`
	NamaBank              Optional[string] `json:"nama_bank" validate:"omitempty,max=128"`
	TanggalJatuhTempoGiro Optional[string] `json:"tanggal_jatuh_tempo_giro" validate:"omitempty,datetime=2006-01-02"`
	Jumlah                Optional[string] `json:"jumlah" validate:"omitempty,numeric,max=21"`
	Keterangan            Optional[string] `json:"keterangan" validate:"omitempty,max=1000"`
}

// ReplacePembayaranUtangAlokasiRequest swaps the whole allocation set of a DRAFT.
//
// Wholesale replacement rather than per-row CRUD, for the same reason a purchase's lines
// are: the split of one payment across invoices is one decision, and a partial edit
// leaves the header's jumlah_dialokasikan disagreeing with its own rows between
// requests.
//
// An empty array is accepted here and means "allocate none of it" — unlike a document's
// detail lines, which cannot be empty. A payment with no allocation is a credit sitting
// with the supplier, which is a real thing.
type ReplacePembayaranUtangAlokasiRequest struct {
	ID      int64                           `json:"-" validate:"required,gt=0"`
	ActorID int64                           `json:"-" validate:"required,gt=0"`
	Alokasi []PembayaranUtangAlokasiRequest `json:"alokasi" validate:"omitempty,max=500,dive"`
}

type PostingPembayaranUtangRequest struct {
	ID      int64 `json:"-" validate:"required,gt=0"`
	ActorID int64 `json:"-" validate:"required,gt=0"`
}

type BatalPembayaranUtangRequest struct {
	ID          int64  `json:"-" validate:"required,gt=0"`
	ActorID     int64  `json:"-" validate:"required,gt=0"`
	AlasanBatal string `json:"alasan_batal" validate:"required,max=500"`
}

// CairkanGiroRequest records that a giro actually cleared the bank.
//
// This is the moment the payable drops — not when the giro was handed over. Until then
// the paper is a promise, and treating a promise as a payment is how a supplier ends up
// chasing an invoice the system believes is settled.
type CairkanGiroRequest struct {
	ID          int64  `json:"-" validate:"required,gt=0"`
	ActorID     int64  `json:"-" validate:"required,gt=0"`
	TanggalCair string `json:"tanggal_cair" validate:"required,datetime=2006-01-02"`
}

// TolakGiroRequest records that a giro bounced. It never reduced anything, so nothing
// has to be given back — only the status changes, and the payable was never touched.
type TolakGiroRequest struct {
	ID      int64   `json:"-" validate:"required,gt=0"`
	ActorID int64   `json:"-" validate:"required,gt=0"`
	Alasan  *string `json:"alasan" validate:"omitempty,max=500"`
}

type ListPembayaranUtangRequest struct {
	PageRequest
	Search        string  `query:"search" validate:"omitempty,max=255"`
	Status        string  `query:"status" validate:"omitempty,oneof=DRAFT POSTED BATAL"`
	Metode        string  `query:"metode" validate:"omitempty,oneof=TUNAI TRANSFER GIRO"`
	StatusGiro    string  `query:"status_giro" validate:"omitempty,oneof=BELUM_CAIR CAIR TOLAK"`
	IDSupplier    int64   `query:"id_supplier" validate:"omitempty,gt=0"`
	TanggalDari   *string `query:"tanggal_dari" validate:"omitempty,datetime=2006-01-02"`
	TanggalSampai *string `query:"tanggal_sampai" validate:"omitempty,datetime=2006-01-02"`
}

// UtangSupplierResponse is one open invoice: the working list for building a payment.
type UtangSupplierResponse struct {
	IDPembelian      int64     `json:"id_pembelian"`
	Nomor            string    `json:"nomor"`
	Tanggal          time.Time `json:"tanggal"`
	NoFakturSupplier *string   `json:"no_faktur_supplier"`
	TanggalFaktur    *string   `json:"tanggal_faktur"`
	JenisPembayaran  string    `json:"jenis_pembayaran"`
	StatusPembayaran string    `json:"status_pembayaran"`

	Total string `json:"total"`
	// JumlahDialokasikan counts only effective allocations — POSTED payments, and for
	// giro only those that cleared.
	JumlahDialokasikan string `json:"jumlah_dialokasikan"`
	NilaiKreditRetur   string `json:"nilai_kredit_retur"`
	SisaUtang          string `json:"sisa_utang"`
}

// ListUtangSupplierRequest lists a supplier's open invoices, oldest first.
//
// Oldest first rather than newest, unlike every other list in this API: this one is a
// queue to work through, and the invoice that has been waiting longest is the one to
// settle next.
type ListUtangSupplierRequest struct {
	PageRequest
	IDSupplier int64 `param:"id" validate:"required,gt=0"`
	// TermasukLunas brings back invoices with nothing left owing. Off by default,
	// because the point of this read is what is still open.
	TermasukLunas bool `query:"termasuk_lunas"`
}
