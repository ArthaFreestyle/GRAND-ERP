package entity

import "time"

// Status penerimaan susulan. Same machine as pembelian, and for the same reason:
// this document writes kartu_stok, so who decided the numbers may enter the stock
// ledger is a different question from who counted the box.
const (
	StatusSusulanDraft    = "DRAFT"
	StatusSusulanDiajukan = "DIAJUKAN"
	StatusSusulanPosted   = "POSTED"
	StatusSusulanBatal    = "BATAL"
)

// PenerimaanSusulan maps the penerimaan_susulan header: the second shipment for
// goods that did not arrive with the first.
//
// It adds stock and does not add a payable. The supplier's invoice was issued in
// full with the first delivery and booked in full then, so the only thing still
// outstanding is goods — which is why there is no total, no PPN, and no payment
// status here. TotalNilai is the value of what arrived late, not a bill.
//
// There is no freight either. The value entering stock is the remaining share of
// the original invoice value, so one invoice contributes exactly its own value to
// inventory however many times the goods turn up.
//
// IDSupplier and IDRuang are copied from the purchase rather than chosen: the
// supplier is fixed by the document this is a remainder of, and the room was
// already settled there. Goods that need to move rooms after arrival are a mutasi.
type PenerimaanSusulan struct {
	ID          int64
	Nomor       string
	Tanggal     time.Time
	IDPembelian int64
	IDSupplier  int64
	IDRuang     int64
	Keterangan  *string
	TotalNilai  string
	Status      string

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

	// Not columns of penerimaan_susulan. Filled by the read queries.
	NomorPembelian string
	NamaSupplier   string
	NamaRuang      string
	Detail         []PenerimaanSusulanDetail
}

// PenerimaanSusulanDetail maps one line, pointing at the pembelian_detail row it
// completes — the mirror of retur_pembelian_detail, with the goods moving the other
// way.
//
// Two snapshots taken from different places, deliberately:
//
//   - FaktorKonversi comes from product_satuan now, because the quantity is a fresh
//     observation and may well be counted in a different unit than the invoice line
//     used (five loose pieces short of a line typed in boxes).
//   - HargaPokokSatuanDasar is copied from the source pembelian_detail, because
//     what the goods cost was settled by the invoice and a second delivery does not
//     change it. Copying it is also what makes a purchase and its follow-ups add up
//     to exactly the invoice value.
type PenerimaanSusulanDetail struct {
	ID                    int64
	IDPenerimaanSusulan   int64
	IDPembelianDetail     int64
	QtyInput              string
	IDSatuanInput         int64
	FaktorKonversi        int64
	QtyDasar              int64
	HargaPokokSatuanDasar string
	Nilai                 string

	// Not columns of penerimaan_susulan_detail. Reached through pembelian_detail.
	IDProduct       int64
	KodeBarang      string
	NamaProduct     string
	NamaSatuan      string
	NamaSatuanDasar string
}
