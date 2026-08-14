package entity

import "time"

// Jenis transaksi kartu stok, matching the jenis_transaksi enum from migration
// 000002. Constants rather than literals at each call site: a typo would arrive as
// an invalid-enum error from PostgreSQL naming a type instead of a module.
const (
	JenisTransaksiPembelian         = "PEMBELIAN"
	JenisTransaksiPenerimaanSusulan = "PENERIMAAN_SUSULAN"
	JenisTransaksiReturPembelian    = "RETUR_PEMBELIAN"
	// The two halves of one mutasi line. They always come in pairs, in this order,
	// inside one transaction: goods leave the source room and enter the destination
	// room, and no other document produces either value. Writing one without the other
	// would make stock vanish or appear.
	JenisTransaksiMutasiKeluar        = "MUTASI_KELUAR"
	JenisTransaksiMutasiMasuk         = "MUTASI_MASUK"
	JenisTransaksiPembatalanTransaksi = "PEMBATALAN_TRANSAKSI"
)

// Ref tables recorded on kartu_stok. Paired with ref_id_transaksi they say which
// document produced a row, which is what makes a cancellation able to find the rows
// it has to reverse.
const (
	RefTablePembelian      = "pembelian"
	RefTableSusulan        = "penerimaan_susulan"
	RefTableReturPembelian = "retur_pembelian"
	// RefTableMutasi is the first document to produce two rows per line under one
	// reference — an outgoing one and an incoming one. FindByRef returns both, and a
	// cancellation reverses each of them, which is what keeps the pair summing to zero
	// in quantity.
	RefTableMutasi = "mutasi"
)

// KartuStok maps the kartu_stok table: the only source of truth for stock and
// inventory value. No master table carries a stock column, and stock is never
// computed by summing documents.
//
// The table is append-only, enforced by trigger — UPDATE, DELETE, and TRUNCATE all
// raise. Corrections are new reversing rows that fill IDKartuStokAsal.
//
// Five of these fields are written by the application and then thrown away: the
// kartu_stok_hitung_saldo trigger overwrites StokAwal, StokAkhir,
// HargaPokokSatuan, NilaiKeluar, and NilaiAkhir on every insert. They are here to
// carry what the database computed back to the caller, not to send anything to it.
// See KartuStokRepository.Insert for what the application actually supplies.
//
// Quantities are always in the base unit. QtyInput and IDSatuanInput are an audit
// trail of what the operator typed, nothing more.
//
// The money columns are strings because they are NUMERIC: scanned as ::TEXT so the
// exact decimal PostgreSQL stored survives the trip out. A float64 would round it.
type KartuStok struct {
	ID               int64
	IDBarang         int64
	IDRuang          int64
	TanggalTransaksi time.Time
	JenisTransaksi   string

	StokAwal   int64
	StokMasuk  int64
	StokKeluar int64
	StokAkhir  int64

	// QtyInput is NUMERIC(18,4) and nullable — a reversing row has no operator
	// input behind it.
	QtyInput      *string
	IDSatuanInput *int64

	HargaPokokSatuan string
	NilaiMasuk       string
	NilaiKeluar      string
	NilaiAkhir       string

	RefTable       string
	RefIDTransaksi int64
	// IDKartuStokAsal points at the row this one reverses. Only a correction fills
	// it; an ordinary posting leaves it NULL.
	IDKartuStokAsal *int64
	Keterangan      *string

	CreatedBy int64
	CreatedAt time.Time
}
