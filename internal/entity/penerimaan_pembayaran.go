package entity

import "time"

// Status penerimaan pembayaran. DRAFT -> POSTED -> BATAL, with **no DIAJUKAN** —
// the mirror of pembayaran_utang's shape and for the identical reason. See
// StatusPembayaranUtangDraft: allocation is not append-only, it can be voided and
// every cache recomputed exactly, so there is no residue an approval stage would be
// protecting. The two-person control is still there, one state shorter: whoever
// prepares the document is not whoever releases the customer's credit.
//
// Migration 000024 pins these three strings in a CHECK constraint. Migration 000006
// already implied them by giving this table posted_at, dibatalkan_oleh, and
// alasan_batal but neither diajukan_oleh nor disetujui_oleh.
const (
	StatusPenerimaanPembayaranDraft  = "DRAFT"
	StatusPenerimaanPembayaranPosted = "POSTED"
	StatusPenerimaanPembayaranBatal  = "BATAL"
)

// Metode dan StatusGiro reuse the exact three-and-three vocabulary
// pembayaran_utang uses (MetodePembayaranTunai/Transfer/Giro,
// StatusGiroBelumCair/Cair/Tolak) — money moving the other way changes nothing
// about what a giro is. **An uncashed giro is not a payment** here either: the
// receivable drops only when StatusGiro reaches CAIR.

// PenerimaanPembayaran maps the penerimaan_pembayaran header: money received from a
// customer.
//
// The mirror of PembayaranUtang, with the money flowing the other way. One payment
// settles several notas and one nota may be settled by several payments, which is
// why allocation is its own table rather than a column on penjualan.
//
// JumlahDialokasikan may be **less** than Jumlah, and that is normal rather than an
// error state: the remainder sits as a credit with the customer for a later nota.
// Never force allocation to balance exactly.
//
// Money columns are strings because they are NUMERIC: read as ::TEXT so the exact
// decimal survives, since a float64 rounds money on the way out.
type PenerimaanPembayaran struct {
	ID          int64
	Nomor       string
	Tanggal     time.Time
	IDPelanggan int64

	Metode      string
	NoReferensi *string
	NamaBank    *string
	// Giro columns, all NULL unless Metode is GIRO — enforced by
	// penerimaan_pembayaran_giro_check.
	TanggalJatuhTempoGiro *time.Time
	TanggalCair           *time.Time
	StatusGiro            *string

	Jumlah string
	// JumlahDialokasikan is a cache over the allocation rows, always recomputed and
	// never set from a form.
	JumlahDialokasikan string
	Status             string
	Keterangan         *string

	CreatedBy int64
	CreatedAt time.Time
	PostedAt  *time.Time

	DibatalkanOleh *int64
	AlasanBatal    *string

	// Not columns of penerimaan_pembayaran. Filled by the read queries.
	NamaPelanggan string
	Alokasi       []PembayaranAlokasi
}

// PembayaranAlokasi maps one allocation: how much of a payment goes against one
// sales nota.
//
// It carries no snapshot of the nota's figures on purpose, the same reasoning
// PembayaranUtangAlokasi documents: an allocation is an amount of money pointed at
// a document, not a valuation of it, and the document it points at is POSTED and
// can no longer change its own total. What has to stay honest is the arithmetic
// across rows, and that is recomputed rather than frozen.
type PembayaranAlokasi struct {
	ID                     int64
	IDPenerimaanPembayaran int64
	IDPenjualan            int64
	Jumlah                 string
	CreatedAt              time.Time

	// Not columns of pembayaran_alokasi. Reached through penjualan, so a payment
	// screen can name the notas it settles without a query per row.
	NomorPenjualan   string
	Total            string
	StatusPembayaran string
}
