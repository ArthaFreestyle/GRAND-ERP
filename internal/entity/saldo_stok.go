package entity

import "time"

// SaldoStok is the running balance of one product in one room: the last row of a
// kartu_stok chain, read rather than recomputed.
//
// It is a projection, not a table. Stock has never been stored anywhere but
// kartu_stok, and this is the first thing in the codebase that reads it back —
// pembelian and penerimaan_susulan only ever add, and retur_pembelian takes its quota
// from an invoice line rather than from a balance. Mutasi is the first document whose
// input screen cannot be used without knowing what a room actually holds, and
// penjualan, pemakaian, and stok opname will all want the same answer.
//
// A pair with no rows at all is a balance of zero, not a missing record. That is the
// same reading kartu_stok_hitung_saldo takes when it COALESCEs the previous row to
// zero, and the same shape periode uses for a month nobody has closed: absence is a
// value, not a 404.
//
// Money is a string because it is NUMERIC. A float64 would round the inventory
// valuation on the way out.
//
// Nothing read through this may be used as a guard. The balance is decided inside the
// trigger, under an advisory lock, precisely so no reader can get in front of it —
// what a read like this buys is an error message naming the shortfall, and an input
// screen that can show a number before anyone types.
type SaldoStok struct {
	IDBarang int64
	IDRuang  int64

	StokAkhir        int64
	NilaiAkhir       string
	HargaPokokSatuan string

	// TanggalTransaksi is when the last movement was dated, and TerakhirDiperbarui
	// when it was recorded. They differ whenever a document was typed in late, which
	// is the ordinary case.
	//
	// Both are nil for a pair that has never moved: there is no last row to date.
	TanggalTransaksi   *time.Time
	TerakhirDiperbarui *time.Time

	// NamaRuang is not a column of kartu_stok. Filled only by the per-product read,
	// which joins ruang so a screen can name the room it is about to take goods out of.
	NamaRuang string
}
