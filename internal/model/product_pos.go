package model

// PosProductResponse is one product row for the POS catalog screen — isu #11,
// GET /api/v1/pos/product. Nested, not flat: one row per product with satuan as an
// array, so a product with three units does not eat three slots of a 20-row page and
// total_item counts products, not (product, satuan) combinations.
//
// Deliberately absent, and left out rather than merely forgotten: created_at,
// created_by, updated_at, updated_by (audit trail no cashier action depends on),
// is_aktif (always true here — see below), id_satuan_dasar (already implied by the
// satuan row with faktor == 1), stok_minimum (a re-order threshold, an INVENTARIS
// decision), and every past price version (only what applies today is a POS
// concern). Most of all: harga_pokok_satuan and nilai_akhir never appear anywhere in
// this response. This screen faces the counter and is open to any authenticated
// caller, and HPP is margin — GET /product/{id}/stok still carries it for whoever is
// actually entitled to it.
type PosProductResponse struct {
	ID         int64  `json:"id"`
	KodeBarang string `json:"kode_barang"`
	Nama       string `json:"nama"`

	// Satuan is never empty and never null: every product carries at least its base
	// unit, guaranteed at Create.
	Satuan []PosProductSatuanResponse `json:"satuan"`

	// StokAkhir is in base units, always — kartu_stok knows no other unit;
	// converting to a display unit is the client's job, and faktor is sent for
	// exactly that. This is a reading, not a guarantee: by the time a client acts on
	// it the balance may already have moved, and the refusal that matters comes from
	// the kartu_stok trigger deciding under an advisory lock. An out-of-stock
	// product still appears, with stok_akhir: 0 — hiding it would tell a cashier the
	// product does not exist, when the true answer is that it is out.
	StokAkhir int64 `json:"stok_akhir"`
}

// PosProductSatuanResponse is one conversion unit as sold at the counter.
//
// IDHargaJual and Harga are both null together when no price version is in force
// for this product and satuan on the requested date — a price is a proposal here,
// never a requirement, and filtering out priceless products would quietly undo that
// decision. IDHargaJual rides along because penjualan_detail.id_harga_jual
// references it: without it a POS screen would know the number but not which
// price-list version it came from, and the trace isu #10 needs would be lost at the
// earliest possible point.
type PosProductSatuanResponse struct {
	IDSatuan       int64  `json:"id_satuan"`
	NamaSatuan     string `json:"nama_satuan,omitempty"`
	Faktor         int64  `json:"faktor"`
	IsDefaultInput bool   `json:"is_default_input"`

	IDHargaJual *int64  `json:"id_harga_jual"`
	Harga       *string `json:"harga"`
}

// ListPosProductRequest asks for one page of the POS catalog.
//
// IDRuang is required, unlike every other list in this API: a cashier always knows
// which room they are standing in, and making it optional would mean two response
// shapes for one endpoint. It is validated against ruang before anything else runs —
// a mistyped id_ruang that silently answered zero stock on every row would be an
// expensive bug to notice.
//
// There is deliberately no is_aktif parameter: this endpoint never lists a retired
// product, and offering a switch to reveal one would be offering a way to sell it.
type ListPosProductRequest struct {
	PageRequest
	IDRuang int64  `query:"id_ruang" validate:"required,gt=0"`
	Search  string `query:"search" validate:"omitempty,max=255"`
	// Tanggal defaults to today in WIB when omitted, resolved by tanggalHargaJual —
	// the same decision isu #8 made, applied here rather than re-decided. Useful for
	// tests and for typing a backdated document.
	Tanggal string `query:"tanggal" validate:"omitempty,datetime=2006-01-02"`
}
