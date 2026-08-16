package entity

import "time"

// PiutangPelanggan is a projection, not a table: one POSTED KREDIT nota that a
// customer still owes money on.
//
// It follows the UtangSupplier shape — the mirror image on the receivable side, isu
// #10 fase 2. No migration, no DTO to fill in, nothing that can fall out of step:
// every figure it reports is already recorded on documents that were posted anyway.
// Unlike UtangSupplier there is no return-credit and no allocation to subtract yet —
// retur_penjualan and penerimaan_pembayaran are both out of scope for this issue —
// so SisaPiutang is simply Total for now. The day either of those is built, this
// shape gains the same two figures UtangSupplier already carries and Total stops
// being the whole answer.
type PiutangPelanggan struct {
	IDPenjualan int64
	Nomor       string
	Tanggal     time.Time
	Total       string
	// SisaPiutang is reported as its own figure, not left for the client to
	// recompute, so the day a credit or an allocation enters this sum the shape
	// does not have to change — only what feeds it.
	SisaPiutang string
}
