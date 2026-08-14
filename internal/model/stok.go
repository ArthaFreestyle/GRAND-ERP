package model

import "time"

// StokRuangResponse is what one product holds in one room right now.
//
// Every money figure is a decimal string. NUMERIC through a JSON float loses cents,
// and nilai_akhir is part of the inventory valuation.
//
// This is a read of kartu_stok, which is the only source of truth for stock — no
// master table carries a stock column and nothing here sums documents. It is also not
// a guarantee: by the time a client acts on it another posting may have moved the
// balance, and the refusal that matters comes from the kartu_stok trigger, which
// decides under an advisory lock. Show it, do not gate on it.
type StokRuangResponse struct {
	IDRuang   int64  `json:"id_ruang"`
	NamaRuang string `json:"nama_ruang,omitempty"`

	// StokAkhir is in base units, always. kartu_stok knows no other unit.
	StokAkhir int64 `json:"stok_akhir"`
	// NilaiAkhir is the inventory value of that quantity, and HargaPokokSatuan the
	// moving average behind it. Stock at zero forces the value to exactly zero, so the
	// two cannot drift apart into rounding residue.
	NilaiAkhir       string `json:"nilai_akhir"`
	HargaPokokSatuan string `json:"harga_pokok_satuan"`

	// TanggalTransaksi is when the last movement was dated; TerakhirDiperbarui when it
	// was recorded. They differ whenever a document was typed in late, which is
	// ordinary. Both are null only for a room with no movements, which this endpoint
	// does not list.
	TanggalTransaksi   *time.Time `json:"tanggal_transaksi"`
	TerakhirDiperbarui *time.Time `json:"terakhir_diperbarui"`
}

// ListStokProductRequest asks where a product's stock currently sits.
//
// No pagination, unlike every other list in this API. The answer is one row per room
// the product has moved through, ruang is a master table with a handful of rows, and
// every caller — the mutasi entry screen first, then penjualan and pemakaian — wants
// all of them at once to choose a source room. Paging that would be ceremony.
//
// IDProduct comes from the path, never the query string, so the two cannot disagree.
type ListStokProductRequest struct {
	IDProduct int64 `json:"-" validate:"required,gt=0"`
}
