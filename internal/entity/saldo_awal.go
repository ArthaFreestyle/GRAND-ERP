package entity

import "time"

// Status saldo_awal — the same vocabulary pembelian and penerimaan_susulan use,
// guarded by a CHECK (migration 000030).
//
// DRAFT -> DIAJUKAN -> POSTED -> BATAL, with DIAJUKAN -> DRAFT on rejection. Unlike
// mutasi, the approval stage stays: mutasi drops it because its mistake is cheap
// (total stock and value do not move), and that reasoning is exactly reversed here —
// this document CREATES inventory value out of nothing, so the two-person control is
// mandatory.
const (
	StatusSaldoAwalDraft    = "DRAFT"
	StatusSaldoAwalDiajukan = "DIAJUKAN"
	StatusSaldoAwalPosted   = "POSTED"
	StatusSaldoAwalBatal    = "BATAL"
)

// SaldoAwal maps the saldo_awal header: the opening stock of a unit_kerja that has
// just migrated into this system and already holds goods on its shelves.
//
// It is the only document in this project whose cost is TYPED. Everywhere else the
// cost is derived (pembelian), copied from a source document (penerimaan_susulan,
// retur_pembelian), read back from RETURNING (mutasi, pemakaian, penjualan) or taken
// from the room's own average (stok_opname surplus). A migration is the one situation
// with no source document to copy from — which is why what matters is the fence
// (SaldoAwalUseCase.Posting: one (barang, ruang) for life), not the workflow.
//
// There is no supplier, no payable and no billed total: no counterparty at all. The
// only money figure is the value entering inventory, TotalNilai.
type SaldoAwal struct {
	ID      int64
	Nomor   string
	Tanggal time.Time
	IDRuang int64
	// Alasan is the only record of why inventory value was created without a
	// document behind it. Required by the usecase and never clearable by a PATCH —
	// a policy, not a constraint (the keterangan_selisih precedent).
	Alasan string
	Status string

	// TotalNilai is nullable and only answers something once POSTED: it is summed off
	// every line's NilaiMasuk once, right after the posting loop.
	TotalNilai *string

	CreatedBy    int64
	CreatedAt    time.Time
	DiajukanOleh *int64
	DiajukanPada *time.Time
	// DisetujuiOleh/DisetujuiPada are stamped by Posting — the approver is whoever
	// posts, the shape pembelian follows.
	DisetujuiOleh *int64
	DisetujuiPada *time.Time
	PostedAt      *time.Time

	DibatalkanOleh *int64
	AlasanBatal    *string
	// AlasanTolak is the reason the last rejection gave; cleared again by Ajukan.
	AlasanTolak *string

	// Not columns of saldo_awal. Filled by the read queries.
	NamaRuang string
	// IDUnitKerjaRuang is IDRuang's own unit_kerja — isu #12 fase 6 read scoping,
	// riding on the join the query already makes for the room's name.
	IDUnitKerjaRuang int64
	Detail           []SaldoAwalDetail
}

// SaldoAwalDetail maps one line: one product, its typed quantity and typed unit price.
//
// NilaiMasuk is qty_input × harga_satuan_input, computed from the two figures the
// operator typed and rounded once. HargaPokokSatuanDasar is harga / faktor_konversi,
// derived only so the line can be read per base unit — the value entering stock never
// passes through that division, so no cent is lost.
type SaldoAwalDetail struct {
	ID                    int64
	IDSaldoAwal           int64
	IDProduct             int64
	QtyInput              string
	IDSatuanInput         int64
	FaktorKonversi        int64
	QtyDasar              int64
	HargaSatuanInput      string
	HargaPokokSatuanDasar string
	NilaiMasuk            string

	// IDKartuStok is nil until posting, filled from the incoming row's RETURNING.
	IDKartuStok *int64

	// Not columns of saldo_awal_detail. Filled by the read query.
	KodeBarang      string
	NamaProduct     string
	NamaSatuan      string
	NamaSatuanDasar string
}
