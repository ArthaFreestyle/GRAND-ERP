package entity

import "time"

// Status pembelian. The document walks DRAFT -> DIAJUKAN -> POSTED, and a POSTED
// document can only end at BATAL — never back to an editable state, because its
// kartu_stok rows cannot be withdrawn, only reversed.
//
// Migration 000012 pins these five strings in a CHECK constraint, so a value that
// is not here cannot reach the table.
const (
	StatusPembelianDraft    = "DRAFT"
	StatusPembelianDiajukan = "DIAJUKAN"
	StatusPembelianPosted   = "POSTED"
	StatusPembelianBatal    = "BATAL"
)

// Status penerimaan is a cache over the detail rows: KURANG when any line got less
// than the invoice says. Recomputed at posting, never set from a form.
const (
	StatusPenerimaanLengkap = "LENGKAP"
	StatusPenerimaanKurang  = "KURANG"
)

// Metode alokasi biaya angkut. KOLI is the default since migration 000008; QTY is
// the fallback used when every line's jumlah_koli is zero, which would otherwise
// divide by zero.
const (
	MetodeAlokasiKoli = "KOLI"
	MetodeAlokasiQty  = "QTY"
)

const (
	JenisPembayaranTunai  = "TUNAI"
	JenisPembayaranKredit = "KREDIT"
)

// Status pembayaran is a cache too, recomputed from POSTED allocations and POSTED
// returns. Nothing in this module sets it beyond the initial BELUM — the payables
// module owns it.
const (
	StatusPembayaranBelum    = "BELUM"
	StatusPembayaranSebagian = "SEBAGIAN"
	StatusPembayaranLunas    = "LUNAS"
)

// Pembelian maps the pembelian header.
//
// Money columns are strings because they are NUMERIC: read as ::TEXT so the exact
// decimal survives, since a float64 rounds money on the way out.
//
// Subtotal, PPN, Pembulatan and Total describe the supplier's invoice and are
// driven by qty_faktur. BiayaAngkut is the carrier's bill and is not part of Total:
// it is money owed to the ekspedisi, not to the supplier, and it is allocated into
// cost of goods rather than into the payable.
//
// IDRuang fixes the destination room for every line. One document cannot receive
// into two rooms.
type Pembelian struct {
	ID      int64
	Nomor   string
	Tanggal time.Time

	IDSupplier       int64
	IDRuang          int64
	NoFakturSupplier *string
	TanggalFaktur    *time.Time

	Subtotal   string
	DiskonNota string
	PPN        string
	// PPNDikreditkan false means the PPN becomes part of cost of goods; true means
	// it is input tax and stays out of it.
	PPNDikreditkan bool
	Pembulatan     string
	Total          string

	IDEkspedisi  *int64
	NoResi       *string
	TotalKoli    *string
	TarifPerKoli *string
	BiayaAngkut  string
	// DitanggungSupplier true means freight is already inside the supplier's
	// invoice and must not be allocated a second time.
	DitanggungSupplier  bool
	MetodeAlokasiAngkut string

	JenisPembayaran  string
	StatusPembayaran string
	StatusPenerimaan string
	Status           string

	CreatedBy int64
	CreatedAt time.Time

	DiajukanOleh  *int64
	DiajukanPada  *time.Time
	DisetujuiOleh *int64
	DisetujuiPada *time.Time
	PostedAt      *time.Time

	DibatalkanOleh *int64
	AlasanBatal    *string
	AlasanTolak    *string

	// Not columns of pembelian. Filled by the read queries, the way
	// Product.NamaSatuanDasar is.
	NamaSupplier string
	NamaRuang    string
	Detail       []PembelianDetail
}

// PembelianDetail maps one invoice line.
//
// Two quantities, because the receiving desk compares two different things:
//
//	QtyFaktur / QtyDasar               -- what the paper says, drives the payable
//	QtyDiterima / QtyDiterimaDasar     -- what came out of the box, drives stock
//
// The *Dasar pair is in the base unit and is what reaches kartu_stok; the other two
// are in IDSatuanInput and are what the operator typed.
//
// FaktorKonversi is a snapshot taken from product_satuan when the line is written.
// Master data may change later; this must not, or a document would silently start
// meaning a different quantity than it did when it was posted.
//
// HargaPokokSatuanDasar is nullable because it is only known at posting — empty
// during DRAFT is normal, not missing data.
type PembelianDetail struct {
	ID          int64
	IDPembelian int64
	IDProduct   int64

	QtyFaktur        string
	QtyDiterima      string
	IDSatuanInput    int64
	FaktorKonversi   int64
	QtyDasar         int64
	QtyDiterimaDasar int64
	// QtySusulanDasar is not a column: it is the sum of every POSTED
	// penerimaan_susulan line pointing at this row. What the supplier still owes is
	// QtyDasar - QtyDiterimaDasar - QtySusulanDasar.
	QtySusulanDasar int64

	HargaSatuanInput string
	DiskonBaris      string
	Subtotal         string
	JumlahKoli       string
	AlokasiBiaya     string
	// HargaPokokSatuanDasar = (nilai baris setelah diskon, PPN dan ongkir) /
	// qty_dasar — never harga_satuan_input / faktor_konversi.
	HargaPokokSatuanDasar *string
	KeteranganSelisih     *string

	// Not columns of pembelian_detail.
	NamaProduct     string
	KodeBarang      string
	NamaSatuan      string
	NamaSatuanDasar string
}
