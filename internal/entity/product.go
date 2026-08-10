package entity

import "time"

// Product maps the product table. Referenced by kartu_stok and every transaction
// detail table, so rows are retired with is_aktif rather than deleted.
//
// There is deliberately no stock field. kartu_stok is the only source of truth for
// stock and inventory value; a column here would be a second answer that drifts.
//
// created_by is NOT NULL, unlike supplier.created_by — which is why this module could
// not ship before authentication existed.
type Product struct {
	ID            int64
	KodeBarang    string
	Nama          string
	IDSatuanDasar int64
	StokMinimum   int64
	IsAktif       bool
	CreatedAt     time.Time
	CreatedBy     int64
	UpdatedAt     time.Time
	UpdatedBy     *int64

	// Not columns of product. Filled by the read queries, the way
	// Supplier.NamaPembuat is.
	NamaSatuanDasar string
	Satuan          []ProductSatuan
	HargaJual       []ProductHargaJual
}

// ProductSatuan maps product_satuan: how many base units one alternative unit holds.
//
// Faktor is BIGINT in the schema, so conversions must be whole numbers — a unit
// holding 2.5 base units cannot be represented.
type ProductSatuan struct {
	ID             int64
	IDProduct      int64
	IDSatuan       int64
	Faktor         int64
	IsDefaultInput bool

	// NamaSatuan comes from a join on satuan, not from product_satuan.
	NamaSatuan string
}

// ProductHargaJual maps product_harga_jual, a versioned price per product and unit.
//
// Harga is a string because it is NUMERIC(20,2): money never passes through a float
// on its way in or out.
//
// BerlakuSampai is exclusive and NULL means open-ended, because
// product_harga_jual_no_overlap ranges over daterange(berlaku_dari, berlaku_sampai,
// '[)'). Closing a version means setting it to the next version's start date, not to
// the day before.
type ProductHargaJual struct {
	ID            int64
	IDProduct     int64
	IDSatuan      int64
	Harga         string
	BerlakuDari   time.Time
	BerlakuSampai *time.Time
	CreatedAt     time.Time
	CreatedBy     int64

	NamaSatuan string
}
