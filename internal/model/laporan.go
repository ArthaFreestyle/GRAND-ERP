package model

// Three cross-cutting reports over material that was already computed and stored
// somewhere — isu #22 fase 3. None of the three stores a number of its own.

// NilaiPersediaanResponse is one room's current inventory value.
type NilaiPersediaanResponse struct {
	IDRuang    int64  `json:"id_ruang"`
	NamaRuang  string `json:"nama_ruang"`
	TotalNilai string `json:"total_nilai"`
}

// ListNilaiPersediaanRequest asks for the current inventory value, one row per room.
//
// IDRuang, given, narrows the report to a single room. ruang.is_aktif is never a
// filter here, and there is deliberately no parameter for it: a retired room still
// holding stock still holds its value, and hiding it would misstate the balance
// sheet this report exists to state.
type ListNilaiPersediaanRequest struct {
	IDRuang *int64 `query:"id_ruang" validate:"omitempty,gt=0"`

	// AktifIDUnitKerja is filled from the session's active grant by the controller,
	// never from the request — isu #12 fase 6 applied to this new read.
	AktifIDUnitKerja *int64 `query:"-"`
}

// LabaKotorResponse is gross margin for one calendar month ("YYYY-MM").
type LabaKotorResponse struct {
	Bulan          string `json:"bulan"`
	TotalPenjualan string `json:"total_penjualan"`
	TotalHPP       string `json:"total_hpp"`
	LabaKotor      string `json:"laba_kotor"`
}

// ListLabaKotorRequest asks for gross margin grouped by month.
//
// Dari/Sampai bound p.tanggal, both inclusive whole days; omitted, the report covers
// every POSTED nota ever recorded.
type ListLabaKotorRequest struct {
	Dari   *string `query:"dari" validate:"omitempty,datetime=2006-01-02"`
	Sampai *string `query:"sampai" validate:"omitempty,datetime=2006-01-02"`

	// AktifIDUnitKerja is filled from the session's active grant by the controller,
	// never from the request — isu #12 fase 6 applied to this new read.
	AktifIDUnitKerja *int64 `query:"-"`
}

// PergerakanResponse is one (product, room, jenis_transaksi) group's movement inside
// a date range — how much moved in, how much moved out, and by what kind of
// document.
type PergerakanResponse struct {
	IDProduct      int64  `json:"id_product"`
	KodeBarang     string `json:"kode_barang"`
	NamaProduct    string `json:"nama_product"`
	IDRuang        int64  `json:"id_ruang"`
	NamaRuang      string `json:"nama_ruang"`
	JenisTransaksi string `json:"jenis_transaksi"`
	TotalMasuk     int64  `json:"total_masuk"`
	TotalKeluar    int64  `json:"total_keluar"`
}

// ListPergerakanRequest asks for a movement recap grouped by product, room, and
// jenis_transaksi.
//
// The range filters kartu_stok.tanggal_transaksi, which is what surfaces rather than
// hides the deliberate trap this issue names by name: a document posted in one
// period and cancelled in a later one produces a reversing row dated when the
// cancellation happened (isu #6), so it belongs to — and this report places it in —
// the range covering the cancellation, never the range covering the original
// posting.
//
// IDRuang and IDProduct both narrow the recap; either or both may be omitted.
type ListPergerakanRequest struct {
	Dari      *string `query:"dari" validate:"omitempty,datetime=2006-01-02"`
	Sampai    *string `query:"sampai" validate:"omitempty,datetime=2006-01-02"`
	IDRuang   *int64  `query:"id_ruang" validate:"omitempty,gt=0"`
	IDProduct *int64  `query:"id_product" validate:"omitempty,gt=0"`

	// AktifIDUnitKerja is filled from the session's active grant by the controller,
	// never from the request — isu #12 fase 6 applied to this new read.
	AktifIDUnitKerja *int64 `query:"-"`
}
