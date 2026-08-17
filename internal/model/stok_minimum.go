package model

// StokMinimumResponse is one product whose current stock has reached or fallen below
// its own stok_minimum — isu #22 fase 2. TotalStok is in the base unit, summed across
// every room in scope; Selisih is StokMinimum - TotalStok, always >= 0 here, and is
// what the list is ordered by, worst first.
type StokMinimumResponse struct {
	IDProduct   int64  `json:"id_product"`
	KodeBarang  string `json:"kode_barang"`
	NamaProduct string `json:"nama_product"`
	StokMinimum int64  `json:"stok_minimum"`
	TotalStok   int64  `json:"total_stok"`
	Selisih     int64  `json:"selisih"`

	// PerRuang is the per-room breakdown behind TotalStok — never empty for a
	// product that has moved anywhere in scope, and empty only for one that has
	// never moved at all, which is exactly why it appears here in the first place.
	PerRuang []StokRuangResponse `json:"per_ruang"`
}

// ListStokMinimumRequest asks for every active product at or below its own
// stok_minimum.
//
// IDRuang, given, narrows the comparison to a single room's balance rather than the
// sum across every room in scope — for a warehouse deliberately kept near empty
// because its stock lives in a shop instead, comparing that room alone against a
// company-wide minimum would flag it every day for no reason a purchaser can act on.
// Omitted, the comparison is against the total across every room the caller's active
// unit_kerja can see.
type ListStokMinimumRequest struct {
	PageRequest
	IDRuang int64 `query:"id_ruang" validate:"omitempty,gt=0"`

	// AktifIDUnitKerja is filled from the session's active grant by the controller,
	// never from the request — isu #12 fase 6 applied to this new read. Nil means
	// unrestricted.
	AktifIDUnitKerja *int64 `query:"-"`
}
