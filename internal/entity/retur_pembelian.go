package entity

import "time"

// Status retur pembelian. Same machine as pembelian and penerimaan_susulan, and for
// the same reason: this document writes kartu_stok, so who decided the goods may
// leave the stock ledger is a different question from who packed the box.
//
// Migration 000014 pins these four strings in a CHECK constraint.
const (
	StatusReturDraft    = "DRAFT"
	StatusReturDiajukan = "DIAJUKAN"
	StatusReturPosted   = "POSTED"
	StatusReturBatal    = "BATAL"
)

// ReturPembelian maps the retur_pembelian header: goods sent back to the supplier.
//
// It is the mirror of PenerimaanSusulan. Both point at pembelian_detail rows of a
// POSTED purchase and both copy their cost from there; the goods simply move the
// other way. What differs is only the direction and the sign of what it means for
// the payable.
//
// Total is the inventory value withdrawn, computed from the cost copied off the
// source lines — not a figure the caller sends. Read the comment on
// ReturPembelianDetail.Nilai before using it as the credit the supplier owes: they
// are not necessarily the same number.
//
// IDSupplier and IDRuang are copied from the purchase rather than chosen. The
// supplier is whoever sold the goods, and the room is where the purchase put them —
// returning from a different room would be a mutasi first.
type ReturPembelian struct {
	ID          int64
	Nomor       string
	Tanggal     time.Time
	IDPembelian int64
	IDSupplier  int64
	IDRuang     int64
	// Alasan is nullable in the schema but required by the usecase. A return with no
	// stated reason is the one document nobody can audit later: it is the only record
	// of why goods a supplier was already paid for went back.
	Alasan *string
	Total  string
	Status string

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

	// Not columns of retur_pembelian. Filled by the read queries.
	NomorPembelian string
	NamaSupplier   string
	NamaRuang      string
	Detail         []ReturPembelianDetail
}

// ReturPembelianDetail maps one line, pointing at the pembelian_detail row the goods
// came in on.
//
// Two snapshots taken from different places, exactly as penerimaan_susulan does:
//
//   - FaktorKonversi comes from product_satuan now, because the quantity is a fresh
//     count of what is being packed and may well be counted in a different unit than
//     the invoice used (three loose pieces out of a line typed in boxes).
//   - HargaPokokSatuanDasar is copied from the source pembelian_detail, never read
//     off the current moving average. Without that, a purchase and its return do not
//     cancel out — which is what migration 000005 says this table is for.
type ReturPembelianDetail struct {
	ID                    int64
	IDReturPembelian      int64
	IDPembelianDetail     int64
	QtyInput              string
	IDSatuanInput         int64
	FaktorKonversi        int64
	QtyDasar              int64
	HargaPokokSatuanDasar string
	// Nilai = QtyDasar x HargaPokokSatuanDasar: the inventory value this line takes
	// out at the cost the invoice settled.
	//
	// It is NOT what kartu_stok records as leaving. kartu_stok_hitung_saldo overwrites
	// nilai_keluar on every outgoing row with the moving average in force at the time,
	// so when these goods were mixed with older stock the ledger takes out the blended
	// cost instead. Both figures are correct answers to different questions, and
	// neither can be made to be the other.
	Nilai string

	// Not columns of retur_pembelian_detail. Reached through pembelian_detail.
	IDProduct       int64
	KodeBarang      string
	NamaProduct     string
	NamaSatuan      string
	NamaSatuanDasar string
}
