package entity

import "time"

// Isu #37: the stock health score. Like every laporan, nothing here is a table —
// both structs are aggregates read at request time from kartu_stok, product, and
// POSTED stok_opname, and the score built on top of them is never stored.

// KesehatanStokProduk carries the product-level counts behind two of the score's
// four components, for one scope (the active unit_kerja, optionally narrowed to one
// room).
//
// ProdukDinilai/Sehat/Menipis/Habis feed KETERSEDIAAN: active products with
// stok_minimum > 0 that have at least one kartu_stok row in scope, classified by
// their total balance across the scope's rooms against that minimum.
//
// ProdukDipegang/ProdukBerminimum feed CAKUPAN_MINIMUM: active products whose total
// balance in scope is above zero, and how many of those have a minimum configured at
// all.
type KesehatanStokProduk struct {
	ProdukDinilai    int64
	Sehat            int64
	Menipis          int64
	Habis            int64
	ProdukDipegang   int64
	ProdukBerminimum int64
}

// KesehatanStokRuang is one room in scope that currently holds stock, with what the
// other two components need from it.
//
// NilaiPersediaan/NilaiStokMati feed STOK_MATI. NilaiPersediaan is also the weight
// this room carries inside AKURASI_OPNAME.
//
// The opname fields describe the latest POSTED stok_opname on this room whose
// ts_cutoff falls inside the look-back window; all of them are nil when there is
// none, which the score reads as "not counted", not as "counted and perfect".
// NilaiSelisihOpname is surplus plus deficit value, both as absolute amounts;
// NilaiDihitungOpname is the frozen stok_awal of every counted line valued at its
// cutoff row's moving average.
type KesehatanStokRuang struct {
	IDRuang         int64
	NamaRuang       string
	TotalStok       int64
	NilaiPersediaan string
	NilaiStokMati   string

	IDStokOpname        *int64
	NomorOpname         *string
	TsCutoffOpname      *time.Time
	NilaiSelisihOpname  *string
	NilaiDihitungOpname *string
}
