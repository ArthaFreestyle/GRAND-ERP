package entity

// Three cross-cutting reports over material that was already computed and stored
// somewhere — isu #22 fase 3. None of them store a number of their own: every figure
// is summed at read time from kartu_stok or from documents that were posted anyway.

// NilaiPersediaanBaris is one room's current inventory value: the sum of nilai_akhir
// from the last row of every (product, this room) chain.
//
// Retired rooms are never filtered out here on purpose — a room that stopped being
// used can still be holding value, and a report that hides it would misstate the
// balance sheet. That is a deliberate difference from every list endpoint elsewhere
// in this codebase, which does filter is_aktif.
type NilaiPersediaanBaris struct {
	IDRuang    int64
	NamaRuang  string
	TotalNilai string
}

// LabaKotorBaris is gross margin for one calendar month: SUM(total) - SUM(total_hpp)
// over POSTED penjualan, grouped by the month of p.tanggal.
//
// This is the one report in the issue that reads penjualan rather than kartu_stok,
// and that is correct rather than an inconsistency: total_hpp is already a snapshot
// copied from kartu_stok's own RETURNING at posting time and frozen there, so
// re-deriving it from kartu_stok would spend a second, more expensive query to reach
// the same number. retur_penjualan does not exist yet, so nothing here subtracts a
// returned sale's margin back out — the day that module ships, this is where its
// credit has to be wired in.
type LabaKotorBaris struct {
	Bulan          string
	TotalPenjualan string
	TotalHPP       string
	LabaKotor      string
}

// PergerakanBaris is one (product, room, jenis_transaksi) group's movement inside a
// date range — how much moved in and how much moved out, and by what kind of
// document.
//
// The range filters kartu_stok.tanggal_transaksi, never a document's own status or
// the period it was originally posted into. A reversal is dated time.Now(), not the
// original document's date (isu #6), so an old-period document cancelled today
// surfaces in the range covering today — exactly the trap CLAUDE.md names: "anything
// reporting per period must read kartu_stok, never the document status."
type PergerakanBaris struct {
	IDProduct      int64
	KodeBarang     string
	NamaProduct    string
	IDRuang        int64
	NamaRuang      string
	JenisTransaksi string
	TotalMasuk     int64
	TotalKeluar    int64
}
