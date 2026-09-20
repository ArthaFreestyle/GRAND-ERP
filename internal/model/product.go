package model

import "time"

type ProductResponse struct {
	ID              int64  `json:"id"`
	KodeBarang      string `json:"kode_barang"`
	Nama            string `json:"nama"`
	IDSatuanDasar   int64  `json:"id_satuan_dasar"`
	NamaSatuanDasar string `json:"nama_satuan_dasar,omitempty"`
	StokMinimum     int64  `json:"stok_minimum"`
	IsAktif         bool   `json:"is_aktif"`

	// Satuan and HargaJual are filled on detail reads only; a list would need a
	// query per row otherwise. Always arrays when present, never null.
	Satuan    []ProductSatuanResponse    `json:"satuan,omitempty"`
	HargaJual []ProductHargaJualResponse `json:"harga_jual,omitempty"`

	// UnitKerja is the product's catalog membership, filled on detail reads only. A
	// pointer to a slice rather than a slice: nil (a list read, where it is not
	// fetched) drops the key, while a pointer to an empty slice — a product carried by
	// no unit at all — still says `[]`, which `omitempty` on a plain slice cannot.
	UnitKerja *[]ProductUnitKerjaResponse `json:"unit_kerja,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	CreatedBy int64     `json:"created_by"`
	UpdatedAt time.Time `json:"updated_at"`
	UpdatedBy *int64    `json:"updated_by,omitempty"`
}

// ProductUnitKerjaResponse is one unit whose catalog holds the product. IsAktif is the
// unit's, not the membership's — a retired unit's membership is still listed, so it
// can still be seen and removed.
type ProductUnitKerjaResponse struct {
	IDUnitKerja int64   `json:"id_unit_kerja"`
	Kode        *string `json:"kode,omitempty"`
	Nama        string  `json:"nama"`
	IsAktif     bool    `json:"is_aktif"`
}

type ProductSatuanResponse struct {
	ID             int64  `json:"id"`
	IDSatuan       int64  `json:"id_satuan"`
	NamaSatuan     string `json:"nama_satuan,omitempty"`
	Faktor         int64  `json:"faktor"`
	IsDefaultInput bool   `json:"is_default_input"`
}

// ProductHargaJualResponse carries harga as a decimal string so no float ever touches
// money. berlaku_sampai is exclusive; null means open-ended.
type ProductHargaJualResponse struct {
	ID            int64     `json:"id"`
	IDSatuan      int64     `json:"id_satuan"`
	NamaSatuan    string    `json:"nama_satuan,omitempty"`
	Harga         string    `json:"harga"`
	BerlakuDari   string    `json:"berlaku_dari"`
	BerlakuSampai *string   `json:"berlaku_sampai"`
	CreatedAt     time.Time `json:"created_at"`
	CreatedBy     int64     `json:"created_by"`
}

// CreateProductRequest creates a product and its conversion units in one go.
//
// Satuan may be omitted entirely: the base unit is registered automatically with
// faktor = 1 from id_satuan_dasar, so a product is never left without one. Listing the
// base unit here again is allowed and collapses into that single row rather than
// erroring.
//
// ActorID is filled from the session by the controller, never from the body —
// product.created_by is NOT NULL and letting a caller name it would let anyone write
// history under someone else's id.
type CreateProductRequest struct {
	ActorID       int64                        `json:"-" validate:"required,gt=0"`
	KodeBarang    string                       `json:"kode_barang" validate:"required,max=64"`
	Nama          string                       `json:"nama" validate:"required,max=255"`
	IDSatuanDasar int64                        `json:"id_satuan_dasar" validate:"required,gt=0"`
	StokMinimum   int64                        `json:"stok_minimum" validate:"min=0"`
	Satuan        []CreateProductSatuanRequest `json:"satuan" validate:"omitempty,max=32,dive"`

	// AktifIDUnitKerja is filled from the session's active grant by the controller,
	// never from the body. It decides which catalog the product starts in: a session
	// active in a unit starts it in that unit only; nil (a global session) starts it in
	// every active unit. There is deliberately no body field for this — narrowing or
	// widening afterwards is PUT /product/{id}/unit-kerja.
	AktifIDUnitKerja *int64 `json:"-"`
}

// CreateProductSatuanRequest is one conversion row.
//
// Faktor is an integer because product_satuan.faktor is BIGINT. A unit worth 2.5 base
// units cannot be expressed, and rounding it silently would corrupt stock arithmetic.
type CreateProductSatuanRequest struct {
	IDSatuan       int64 `json:"id_satuan" validate:"required,gt=0"`
	Faktor         int64 `json:"faktor" validate:"required,gt=0"`
	IsDefaultInput bool  `json:"is_default_input"`
}

type GetProductRequest struct {
	ID int64 `param:"id" validate:"required,gt=0"`
}

// UpdateProductRequest patches only what is present. kode_barang is absent on purpose:
// it identifies the item across every document that references it, and renaming it
// silently rewrites what those documents appear to be about. Retire the product and
// create a new one instead.
//
// id_satuan_dasar is absent for a harder reason — changing it would invalidate every
// faktor in product_satuan and every quantity already posted to kartu_stok in the old
// base unit.
type UpdateProductRequest struct {
	ID          int64            `json:"-" validate:"required,gt=0"`
	ActorID     int64            `json:"-" validate:"required,gt=0"`
	Nama        Optional[string] `json:"nama" validate:"omitempty,max=255"`
	StokMinimum Optional[int64]  `json:"stok_minimum" validate:"omitempty,min=0"`
	IsAktif     Optional[bool]   `json:"is_aktif"`
}

// SetProductUnitKerjaRequest replaces the whole set of unit catalogs a product is in.
//
// Replace, not add/remove — the same rule grants and product lines follow: `[]` empties
// it, a list leaves exactly those, and an absent or null field is rejected (`required`
// fails on a nil slice but passes an empty one) because there is no "leave alone" for
// the only field this endpoint has. A unit already holding the product may be kept even
// if it has since been retired; only a NEW membership must name an active unit.
type SetProductUnitKerjaRequest struct {
	IDProduct   int64   `json:"-" validate:"required,gt=0"`
	ActorID     int64   `json:"-" validate:"required,gt=0"`
	IDUnitKerja []int64 `json:"id_unit_kerja" validate:"required,max=64,dive,gt=0"`
}

// AddProductSatuanRequest adds one conversion unit to an existing product.
type AddProductSatuanRequest struct {
	IDProduct      int64 `json:"-" validate:"required,gt=0"`
	ActorID        int64 `json:"-" validate:"required,gt=0"`
	IDSatuan       int64 `json:"id_satuan" validate:"required,gt=0"`
	Faktor         int64 `json:"faktor" validate:"required,gt=0"`
	IsDefaultInput bool  `json:"is_default_input"`
}

// AddProductHargaJualRequest opens a new price version.
//
// Any version still open for the same product and unit is closed at berlaku_dari, in
// the same transaction. Since the range is '[)', closing at that date leaves no gap
// and no overlap — the old price covers up to it, the new one from it.
//
// Harga is a string so no float touches money.
type AddProductHargaJualRequest struct {
	IDProduct   int64  `json:"-" validate:"required,gt=0"`
	ActorID     int64  `json:"-" validate:"required,gt=0"`
	IDSatuan    int64  `json:"id_satuan" validate:"required,gt=0"`
	Harga       string `json:"harga" validate:"required,numeric,max=21"`
	BerlakuDari string `json:"berlaku_dari" validate:"required,datetime=2006-01-02"`
}

type ListProductRequest struct {
	PageRequest
	Search string `query:"search" validate:"omitempty,max=255"`
	// Nil lists every product; set it to filter on is_aktif.
	IsAktif *bool `query:"is_aktif"`
}

// HargaJualBerlakuResponse is one satuan's price version in force on the requested
// date — isu #8 fase 1. id_harga_jual is what a sales screen has to send back as
// penjualan_detail.id_harga_jual; the rest is what it displays.
type HargaJualBerlakuResponse struct {
	IDHargaJual   int64   `json:"id_harga_jual"`
	IDSatuan      int64   `json:"id_satuan"`
	NamaSatuan    string  `json:"nama_satuan,omitempty"`
	Harga         string  `json:"harga"`
	BerlakuDari   string  `json:"berlaku_dari"`
	BerlakuSampai *string `json:"berlaku_sampai"`
}

// ListHargaJualBerlakuRequest asks which price version is in force for a product, one
// row per satuan, on one date.
//
// Tanggal is a plain calendar date the caller names directly (YYYY-MM-DD), so there is
// no timezone to resolve here — that question only exists once a TIMESTAMPTZ has to be
// cut down to a date, which is what tanggalHargaJual in the usecase package is for.
// Omitted, Tanggal defaults to today in WIB, resolved the same way.
//
// IDProduct comes from the path, never the query string, so the two cannot disagree.
type ListHargaJualBerlakuRequest struct {
	IDProduct int64  `json:"-" validate:"required,gt=0"`
	Tanggal   string `query:"tanggal" validate:"omitempty,datetime=2006-01-02"`
}

// UpdateProductHargaJualRequest corrects the price on an existing version — isu #8
// fase 2. Only harga: id_satuan and berlaku_dari would shift what date range the
// version covers and can collide with a neighbour, so that correction is
// delete-and-retype rather than a field this DTO exposes.
type UpdateProductHargaJualRequest struct {
	ID        int64  `json:"-" validate:"required,gt=0"`
	IDProduct int64  `json:"-" validate:"required,gt=0"`
	Harga     string `json:"harga" validate:"required,numeric,max=21"`
}

// DeleteProductHargaJualRequest removes a price version outright — isu #8 fase 2, the
// third exception to "master data has no DELETE". Both ids come from the path.
type DeleteProductHargaJualRequest struct {
	ID        int64 `json:"-" validate:"required,gt=0"`
	IDProduct int64 `json:"-" validate:"required,gt=0"`
}

// DaftarHargaJualResponse is one product+satuan row in the cross-product price list —
// isu #8 fase 3. The harga_jual fields are all nullable together: nil means this
// product has no version in force for that satuan on the requested date, including a
// product that has never had a price at all.
type DaftarHargaJualResponse struct {
	IDProduct   int64  `json:"id_product"`
	KodeBarang  string `json:"kode_barang"`
	NamaProduct string `json:"nama_product"`
	IsAktif     bool   `json:"is_aktif"`

	IDSatuan   *int64  `json:"id_satuan"`
	NamaSatuan *string `json:"nama_satuan"`

	IDHargaJual   *int64  `json:"id_harga_jual"`
	Harga         *string `json:"harga"`
	BerlakuDari   *string `json:"berlaku_dari"`
	BerlakuSampai *string `json:"berlaku_sampai"`
}

// ListDaftarHargaJualRequest asks for the price list across every product — isu #8
// fase 3. It is what gets printed as a price list and what finds a product with no
// price registered at all, a question today only answerable by opening products one
// at a time.
type ListDaftarHargaJualRequest struct {
	PageRequest
	Search  string `query:"search" validate:"omitempty,max=255"`
	Tanggal string `query:"tanggal" validate:"omitempty,datetime=2006-01-02"`
	// Nil lists every product; set it to filter on is_aktif.
	IsAktif *bool `query:"is_aktif"`
}
