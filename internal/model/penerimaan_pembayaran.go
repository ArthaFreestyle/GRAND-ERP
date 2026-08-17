package model

import "time"

// PenerimaanPembayaranResponse is one payment received from a customer.
//
// Every money figure is a decimal string, not a JSON number: NUMERIC(20,2) through
// a float loses cents, and these are the amounts a bank reconciliation is done
// against.
//
// jumlah_dialokasikan may be less than jumlah, and that is normal rather than an
// error: the remainder sits as a credit with the customer for a later nota.
type PenerimaanPembayaranResponse struct {
	ID            int64     `json:"id"`
	Nomor         string    `json:"nomor"`
	Tanggal       time.Time `json:"tanggal"`
	IDPelanggan   int64     `json:"id_pelanggan"`
	NamaPelanggan string    `json:"nama_pelanggan,omitempty"`

	Metode      string  `json:"metode"`
	NoReferensi *string `json:"no_referensi"`
	NamaBank    *string `json:"nama_bank"`
	// Giro fields, all null unless metode is GIRO.
	TanggalJatuhTempoGiro *string `json:"tanggal_jatuh_tempo_giro"`
	TanggalCair           *string `json:"tanggal_cair"`
	// StatusGiro drives whether this payment reduces anything at all: an uncashed
	// giro is not a payment. BELUM_CAIR and TOLAK leave every receivable
	// untouched.
	StatusGiro *string `json:"status_giro"`

	Jumlah string `json:"jumlah"`
	// JumlahDialokasikan is recomputed from the allocation rows, never set from a
	// form.
	JumlahDialokasikan string `json:"jumlah_dialokasikan"`
	// SisaBelumDialokasikan = jumlah - jumlah_dialokasikan: money received that is
	// not yet pointed at a nota. Reported rather than left to the client to
	// subtract, because it is the figure that says whether this payment still has
	// room in it.
	SisaBelumDialokasikan string  `json:"sisa_belum_dialokasikan"`
	Status                string  `json:"status"`
	Keterangan            *string `json:"keterangan"`

	// Alokasi is filled on detail reads only; a list would need a query per row.
	Alokasi []PembayaranAlokasiResponse `json:"alokasi,omitempty"`

	CreatedBy int64      `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
	PostedAt  *time.Time `json:"posted_at"`

	DibatalkanOleh *int64  `json:"dibatalkan_oleh"`
	AlasanBatal    *string `json:"alasan_batal"`
}

// PembayaranAlokasiResponse is one allocation, naming the nota it settles.
type PembayaranAlokasiResponse struct {
	ID             int64  `json:"id"`
	IDPenjualan    int64  `json:"id_penjualan"`
	NomorPenjualan string `json:"nomor_penjualan,omitempty"`
	// Total and StatusPembayaran describe the nota as it stands now, so a payment
	// screen can show what it settled and what became of it.
	Total            string    `json:"total_penjualan,omitempty"`
	StatusPembayaran string    `json:"status_pembayaran,omitempty"`
	Jumlah           string    `json:"jumlah"`
	CreatedAt        time.Time `json:"created_at"`
}

// CreatePenerimaanPembayaranRequest opens a DRAFT payment with its allocations.
//
// The document is always created as DRAFT whatever the caller says — status is not
// in this DTO at all. Nothing is settled until posting, and posting is a separate,
// differently authorized call.
//
// alokasi may be omitted or empty: money can legitimately be received from a
// customer before it is decided which notas it covers, and the remainder then sits
// as a credit. That is the same rule as jumlah_dialokasikan being allowed below
// jumlah.
type CreatePenerimaanPembayaranRequest struct {
	ActorID int64 `json:"-" validate:"required,gt=0"`

	// AktifIDUnitKerja is filled from the session's active grant by the
	// controller, never from the body — isu #21 fase 1. This module has no
	// room of its own, so it is what a document number is keyed to instead of
	// a unit read off id_ruang; nil (a global grant) falls back to the
	// pre-existing global series.
	AktifIDUnitKerja *int64 `json:"-"`

	IDPelanggan int64  `json:"id_pelanggan" validate:"required,gt=0"`
	Tanggal     string `json:"tanggal" validate:"required,datetime=2006-01-02"`

	Metode      string  `json:"metode" validate:"required,oneof=TUNAI TRANSFER GIRO"`
	NoReferensi *string `json:"no_referensi" validate:"omitempty,max=64"`
	NamaBank    *string `json:"nama_bank" validate:"omitempty,max=128"`
	// TanggalJatuhTempoGiro is only accepted for metode GIRO, and rejected
	// otherwise — a due date on a cash payment describes nothing.
	TanggalJatuhTempoGiro *string `json:"tanggal_jatuh_tempo_giro" validate:"omitempty,datetime=2006-01-02"`

	Jumlah     string  `json:"jumlah" validate:"required,numeric,max=21"`
	Keterangan *string `json:"keterangan" validate:"omitempty,max=1000"`

	Alokasi []PembayaranAlokasiRequest `json:"alokasi" validate:"omitempty,max=500,dive"`
}

// PembayaranAlokasiRequest points part of a payment at one sales nota.
//
// The nota must be POSTED, KREDIT, and must belong to the payment's customer. A
// DRAFT nota is a typed page rather than a receivable and a BATAL one was
// withdrawn — neither can be collected against. A TUNAI nota never became a
// receivable in the first place: the money already changed hands at the counter.
type PembayaranAlokasiRequest struct {
	IDPenjualan int64  `json:"id_penjualan" validate:"required,gt=0"`
	Jumlah      string `json:"jumlah" validate:"required,numeric,max=21"`
}

type GetPenerimaanPembayaranRequest struct {
	ID int64 `param:"id" validate:"required,gt=0"`
}

// UpdatePenerimaanPembayaranRequest patches the header of a DRAFT.
//
// id_pelanggan is absent: it is whose receivable is being reduced, and changing it
// would leave every allocation pointing at another customer's notas. metode is
// absent too, because it decides whether the giro columns may be filled at all —
// switch it by cancelling and re-entering, which is also what the bank statement
// will show.
type UpdatePenerimaanPembayaranRequest struct {
	ID      int64 `json:"-" validate:"required,gt=0"`
	ActorID int64 `json:"-" validate:"required,gt=0"`

	Tanggal               Optional[string] `json:"tanggal" validate:"omitempty,datetime=2006-01-02"`
	NoReferensi           Optional[string] `json:"no_referensi" validate:"omitempty,max=64"`
	NamaBank              Optional[string] `json:"nama_bank" validate:"omitempty,max=128"`
	TanggalJatuhTempoGiro Optional[string] `json:"tanggal_jatuh_tempo_giro" validate:"omitempty,datetime=2006-01-02"`
	Jumlah                Optional[string] `json:"jumlah" validate:"omitempty,numeric,max=21"`
	Keterangan            Optional[string] `json:"keterangan" validate:"omitempty,max=1000"`
}

// ReplacePenerimaanPembayaranAlokasiRequest swaps the whole allocation set of a
// DRAFT.
//
// Wholesale replacement rather than per-row CRUD, for the same reason a purchase's
// lines are: the split of one payment across notas is one decision, and a partial
// edit leaves the header's jumlah_dialokasikan disagreeing with its own rows
// between requests.
//
// An empty array is accepted here and means "allocate none of it" — unlike a
// document's detail lines, which cannot be empty. A payment with no allocation is a
// credit sitting with the customer, which is a real thing.
type ReplacePenerimaanPembayaranAlokasiRequest struct {
	ID      int64                      `json:"-" validate:"required,gt=0"`
	ActorID int64                      `json:"-" validate:"required,gt=0"`
	Alokasi []PembayaranAlokasiRequest `json:"alokasi" validate:"omitempty,max=500,dive"`
}

type PostingPenerimaanPembayaranRequest struct {
	ID      int64 `json:"-" validate:"required,gt=0"`
	ActorID int64 `json:"-" validate:"required,gt=0"`
}

type BatalPenerimaanPembayaranRequest struct {
	ID          int64  `json:"-" validate:"required,gt=0"`
	ActorID     int64  `json:"-" validate:"required,gt=0"`
	AlasanBatal string `json:"alasan_batal" validate:"required,max=500"`
}

// CairkanGiroPelangganRequest records that a customer's giro actually cleared the
// bank.
//
// This is the moment the receivable drops — not when the giro was handed over.
// Until then the paper is a promise, and treating a promise as a payment is how a
// customer ends up disputing a balance the system believes is settled.
type CairkanGiroPelangganRequest struct {
	ID          int64  `json:"-" validate:"required,gt=0"`
	ActorID     int64  `json:"-" validate:"required,gt=0"`
	TanggalCair string `json:"tanggal_cair" validate:"required,datetime=2006-01-02"`
}

// TolakGiroPelangganRequest records that a customer's giro bounced. It never
// reduced anything, so nothing has to be given back — only the status changes, and
// the receivable was never touched.
type TolakGiroPelangganRequest struct {
	ID      int64   `json:"-" validate:"required,gt=0"`
	ActorID int64   `json:"-" validate:"required,gt=0"`
	Alasan  *string `json:"alasan" validate:"omitempty,max=500"`
}

type ListPenerimaanPembayaranRequest struct {
	PageRequest
	Search        string  `query:"search" validate:"omitempty,max=255"`
	Status        string  `query:"status" validate:"omitempty,oneof=DRAFT POSTED BATAL"`
	Metode        string  `query:"metode" validate:"omitempty,oneof=TUNAI TRANSFER GIRO"`
	StatusGiro    string  `query:"status_giro" validate:"omitempty,oneof=BELUM_CAIR CAIR TOLAK"`
	IDPelanggan   int64   `query:"id_pelanggan" validate:"omitempty,gt=0"`
	TanggalDari   *string `query:"tanggal_dari" validate:"omitempty,datetime=2006-01-02"`
	TanggalSampai *string `query:"tanggal_sampai" validate:"omitempty,datetime=2006-01-02"`
}
