package entity

// StokMinimumBaris is one product whose current stock has reached or fallen below
// its own stok_minimum — isu #22 fase 2, the natural pair to RiwayatBeli: riwayat_beli
// answers who to buy from and at what price, this answers what needs buying at all.
//
// TotalStok is the sum of the last row of every (this product, room) chain in scope —
// every room in the caller's active unit_kerja, or the single room named by id_ruang
// when the caller narrowed to one. A product that has never moved any stock at all
// still belongs here if StokMinimum > 0: TotalStok is then simply 0, not a missing
// record, the same reading kartu_stok takes everywhere else.
//
// A product with StokMinimum == 0 never reaches this struct at all — the filter runs
// in SQL, because 0 is the column's default and means "never set", not "may run out".
type StokMinimumBaris struct {
	IDProduct   int64
	KodeBarang  string
	NamaProduct string
	StokMinimum int64
	TotalStok   int64
}
