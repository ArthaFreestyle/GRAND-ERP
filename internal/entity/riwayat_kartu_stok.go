package entity

import "time"

// RiwayatKartuStok is one row of one (product, room) balance chain, read back for
// display rather than recomputed — isu #22 fase 1.
//
// It is a projection, not a table: kartu_stok is the only source of truth and this
// changes nothing about it. What this adds is what a raw KartuStok row cannot answer
// on its own — NamaSatuanInput (a satuan name, joined in) and NomorDokumen (RefTable +
// RefIDTransaksi translated into the document number an operator can actually read),
// so a client never has to call back per row to make sense of what it received.
//
// Quantities are always in the base unit; QtyInput/NamaSatuanInput are an audit trail
// of what the operator typed, nothing more. Money is a string because it is NUMERIC —
// a float64 rounds the inventory valuation on the way out.
//
// IDKartuStokAsal, non-nil, is what marks a reversing row. A client must be able to
// tell a correction from an ordinary movement without guessing from JenisTransaksi,
// which is shared between a posting and whatever reverses it.
type RiwayatKartuStok struct {
	ID               int64
	TanggalTransaksi time.Time
	JenisTransaksi   string

	StokAwal   int64
	StokMasuk  int64
	StokKeluar int64
	StokAkhir  int64

	QtyInput        *string
	NamaSatuanInput *string

	HargaPokokSatuan string
	NilaiMasuk       string
	NilaiKeluar      string
	NilaiAkhir       string

	RefTable       string
	RefIDTransaksi int64
	// NomorDokumen is nil only when ref_table names a document type this query does
	// not (yet) translate — every writer kartu_stok has today is covered.
	NomorDokumen *string

	IDKartuStokAsal *int64
	Keterangan      *string
}
