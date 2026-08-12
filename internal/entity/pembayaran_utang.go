package entity

import "time"

// Status pembayaran utang. DRAFT -> POSTED -> BATAL, with **no DIAJUKAN**, and that is
// a decision rather than an omission.
//
// The approval stage on pembelian exists because kartu_stok is append-only: a wrong
// posting cannot be repaired, only reversed, and the reversal is valued at whatever the
// moving average has become. Payment allocation is not like that — it can be voided and
// every cache recomputed exactly, with no valuation residue anywhere. Adding DIAJUKAN
// here would be copying a mechanism without its reason. The two-person control is still
// there, one state shorter: whoever prepares the document is not whoever releases the
// money.
//
// Migration 000015 pins these three strings in a CHECK constraint. Migration 000008
// already implied them by giving this table posted_at, dibatalkan_oleh, and
// alasan_batal but neither diajukan_oleh nor disetujui_oleh.
const (
	StatusPembayaranUtangDraft  = "DRAFT"
	StatusPembayaranUtangPosted = "POSTED"
	StatusPembayaranUtangBatal  = "BATAL"
)

// Metode pembayaran. TUNAI and TRANSFER settle the moment the document is posted; GIRO
// does not — see StatusGiro.
const (
	MetodePembayaranTunai    = "TUNAI"
	MetodePembayaranTransfer = "TRANSFER"
	MetodePembayaranGiro     = "GIRO"
)

// Status giro. **An uncashed giro is not a payment**: the payable drops only when this
// reaches CAIR. A cheque that bounces reaches TOLAK and never reduces anything.
//
// Only meaningful for MetodePembayaranGiro, and required there — a giro with no status
// cannot be told apart from one that has cleared, and that is the difference between a
// payable that went down and one that did not.
const (
	StatusGiroBelumCair = "BELUM_CAIR"
	StatusGiroCair      = "CAIR"
	StatusGiroTolak     = "TOLAK"
)

// PembayaranUtang maps the pembayaran_utang header: money paid to a supplier.
//
// The mirror of penerimaan_pembayaran, with the money flowing the other way. One
// payment settles several invoices and one invoice may be settled by several payments,
// which is why allocation is its own table rather than a column on pembelian.
//
// JumlahDialokasikan may be **less** than Jumlah, and that is normal rather than an
// error state: the remainder sits as a credit with the supplier for a later invoice.
// Never force allocation to balance exactly.
//
// Money columns are strings because they are NUMERIC: read as ::TEXT so the exact
// decimal survives, since a float64 rounds money on the way out.
type PembayaranUtang struct {
	ID         int64
	Nomor      string
	Tanggal    time.Time
	IDSupplier int64

	Metode      string
	NoReferensi *string
	NamaBank    *string
	// Giro columns, all NULL unless Metode is GIRO — enforced by
	// pembayaran_utang_giro_check.
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

	// Not columns of pembayaran_utang. Filled by the read queries.
	NamaSupplier string
	Alokasi      []PembayaranUtangAlokasi
}

// PembayaranUtangAlokasi maps one allocation: how much of a payment goes against one
// purchase invoice.
//
// It carries no snapshot of the invoice's figures on purpose. Unlike a stock movement,
// an allocation is not a valuation — it is an amount of money pointed at a document, and
// the document it points at is POSTED and can no longer change its own total. What has to
// stay honest is the arithmetic across rows, and that is recomputed rather than frozen.
type PembayaranUtangAlokasi struct {
	ID                int64
	IDPembayaranUtang int64
	IDPembelian       int64
	Jumlah            string
	CreatedAt         time.Time

	// Not columns of pembayaran_utang_alokasi. Reached through pembelian, so a payment
	// screen can name the invoices it settles without a query per row.
	NomorPembelian   string
	NoFakturSupplier *string
	TotalPembelian   string
	StatusPembayaran string
}
