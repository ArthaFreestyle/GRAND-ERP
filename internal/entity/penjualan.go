package entity

import "time"

// Status penjualan. Migration 000022 pins these three strings in a CHECK, following
// mutasi and pemakaian.
//
// DRAFT -> POSTED -> BATAL, no DIAJUKAN — and the reason is not mutasi's "the stake
// is small". Here the stake is the opposite: goods really leave the shop and money
// really moves, and for a KREDIT nota a receivable is created against someone's
// name. What makes an approval stage unaffordable anyway is the practical
// constraint the issue leads with: a cashier cannot make a customer wait at the
// counter for a supervisor to approve a cash sale typed in seconds. So the
// two-person control moves entirely to the cancellation side — CASHIER creates,
// types lines, and posts; SUPERADMIN is the only one who may void. A mistyped line
// is corrected with a retur_penjualan (a truer trail anyway) or a cancellation by a
// supervisor, never by the cashier who posted it.
const (
	StatusPenjualanDraft  = "DRAFT"
	StatusPenjualanPosted = "POSTED"
	StatusPenjualanBatal  = "BATAL"
)

// Penjualan maps the penjualan header: one nota handed to a walk-in or account
// customer.
//
// It is the fifth document to write kartu_stok and the first to take goods out to an
// outside party with money on the other side of it — pembelian forms a payable,
// mutasi forms nothing at all, this is the first to form a receivable, on a KREDIT
// nota only.
//
// TotalHPP is nullable for the identical reason PemakaianDetail.HPPTotal is: it
// exists only after posting, filled from what the outgoing kartu_stok rows actually
// reported, never computed from a moving average read beforehand. See
// PenjualanUseCase.Posting.
type Penjualan struct {
	ID      int64
	Nomor   string
	Tanggal time.Time
	IDRuang int64
	// IDPelanggan is nullable: a cash sale at the counter needs no customer on
	// file. penjualan_kredit_pelanggan_check is what forbids the one combination
	// that must not happen — KREDIT with no pelanggan, a receivable nobody can be
	// billed.
	IDPelanggan *int64

	Subtotal   string
	DiskonNota string
	Pembulatan string
	Total      string
	// TotalHPP is null until POSTED. It sums HPPTotal off every line, and that
	// figure itself does not exist until the trigger reports it back.
	TotalHPP *string

	JenisPembayaran string
	// StatusPembayaran is a cache, same rule as pembelian's: always recomputed,
	// never set from a form. A TUNAI nota that POSTED is LUNAS by construction —
	// see PenjualanRepository.RecalculateStatusPembayaran — because it has no
	// allocation of its own to sum.
	StatusPembayaran string
	Status           string

	CreatedBy int64
	CreatedAt time.Time
	PostedAt  *time.Time

	DibatalkanOleh *int64
	AlasanBatal    *string

	// Not columns of penjualan. Filled by the read queries.
	NamaRuang     string
	NamaPelanggan *string
	// IDUnitKerjaRuang is IDRuang's own unit_kerja — isu #21 fase 2, the same
	// read-side scoping fase 6 already gave five other modules.
	IDUnitKerjaRuang int64
	Detail           []PenjualanDetail
}

// PenjualanDetail maps one line: one product, what was billed, and — once posted —
// what it cost.
//
// harga_satuan_input is what is actually charged, and it is a snapshot: it is not
// forced to match product_harga_jual, because bargaining happens and the nota is
// what is true. IDHargaJual only records which price-list version the figure came
// from, if any — it may be nil for a manually typed price.
//
// HPPSatuanDasar and HPPTotal follow the exact rule mutasi_detail.HargaPokokSatuanDasar
// and PemakaianDetail.HPPTotal do: nullable because they have no answer before
// posting, filled from the outgoing kartu_stok row's RETURNING, never computed here.
type PenjualanDetail struct {
	ID             int64
	IDPenjualan    int64
	IDProduct      int64
	QtyInput       string
	IDSatuanInput  int64
	FaktorKonversi int64
	QtyDasar       int64
	// IDHargaJual is nullable: a product without a price yet may still be sold at a
	// typed price. When present it is validated against this line's own product,
	// satuan, and the document's date before it is stored — a version that was
	// never actually in force would be a more misleading trail than no reference
	// at all.
	IDHargaJual      *int64
	HargaSatuanInput string
	DiskonBaris      string
	Subtotal         string

	HPPSatuanDasar *string
	HPPTotal       *string

	// Not columns of penjualan_detail. Filled by the read query.
	KodeBarang      string
	NamaProduct     string
	NamaSatuan      string
	NamaSatuanDasar string
}
