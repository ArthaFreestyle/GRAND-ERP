package entity

// ProductPOS is one product row in the POS catalog projection — isu #11. A read
// that is not a module, following riwayat_beli and stok_per_ruang: no table, no
// migration, one query in the repository that already owns product, product_satuan,
// and product_harga_jual, plus the balance batch kartu_stok already exposes.
//
// Deliberately narrower than Product: no audit columns, no id_satuan_dasar (already
// implied by the satuan row with Faktor == 1), no stok_minimum, and no HPP anywhere
// in the chain — this projection faces the counter and is open to any authenticated
// caller, so nothing here may be a figure only INVENTARIS or SUPERADMIN is entitled
// to see.
type ProductPOS struct {
	ID         int64
	KodeBarang string
	Nama       string

	// Satuan is never empty: every product carries at least its base unit,
	// guaranteed by ProductUseCase.Create.
	Satuan []ProductPOSSatuan

	// StokAkhir is in base units, always — kartu_stok knows no other unit. Zero for
	// a room the product has never moved through, same as SaldoBatch's own reading:
	// a missing key is a zero balance, not an error.
	StokAkhir int64
}

// ProductPOSSatuan is one conversion unit as sold at the counter, with the price
// version in force for it on the requested date attached in the same query that
// reads the conversion.
//
// IDHargaJual and Harga are both nil together when no version is in force for this
// product and satuan — a price is a proposal here, never a requirement, the same
// reading FindHargaBerlakuBatch carries everywhere else. A cashier can still sell a
// product with no price row at all.
type ProductPOSSatuan struct {
	IDSatuan       int64
	NamaSatuan     string
	Faktor         int64
	IsDefaultInput bool

	IDHargaJual *int64
	Harga       *string
}
