package converter

import (
	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
)

// dateOnly formats the DATE columns of product_harga_jual. They carry no time and no
// zone, so rendering them as RFC 3339 timestamps would invent both.
const dateOnly = "2006-01-02"

func ProductToResponse(product *entity.Product) *model.ProductResponse {
	response := &model.ProductResponse{
		ID:              product.ID,
		KodeBarang:      product.KodeBarang,
		Nama:            product.Nama,
		IDSatuanDasar:   product.IDSatuanDasar,
		NamaSatuanDasar: product.NamaSatuanDasar,
		StokMinimum:     product.StokMinimum,
		IsAktif:         product.IsAktif,
		CreatedAt:       product.CreatedAt,
		CreatedBy:       product.CreatedBy,
		UpdatedAt:       product.UpdatedAt,
		UpdatedBy:       product.UpdatedBy,
	}

	// Left nil on list reads, where the children are not fetched, so `omitempty`
	// drops the keys rather than claiming the product has no units.
	if product.Satuan != nil {
		response.Satuan = ProductSatuanToResponses(product.Satuan)
	}

	if product.HargaJual != nil {
		response.HargaJual = ProductHargaJualToResponses(product.HargaJual)
	}

	return response
}

func ProductToResponses(list []entity.Product) []model.ProductResponse {
	// make, not var: a nil slice serialises to null instead of [].
	responses := make([]model.ProductResponse, len(list))
	for i := range list {
		responses[i] = *ProductToResponse(&list[i])
	}

	return responses
}

func ProductSatuanToResponse(satuan *entity.ProductSatuan) *model.ProductSatuanResponse {
	return &model.ProductSatuanResponse{
		ID:             satuan.ID,
		IDSatuan:       satuan.IDSatuan,
		NamaSatuan:     satuan.NamaSatuan,
		Faktor:         satuan.Faktor,
		IsDefaultInput: satuan.IsDefaultInput,
	}
}

func ProductSatuanToResponses(list []entity.ProductSatuan) []model.ProductSatuanResponse {
	responses := make([]model.ProductSatuanResponse, len(list))
	for i := range list {
		responses[i] = *ProductSatuanToResponse(&list[i])
	}

	return responses
}

func ProductHargaJualToResponse(harga *entity.ProductHargaJual) *model.ProductHargaJualResponse {
	response := &model.ProductHargaJualResponse{
		ID:          harga.ID,
		IDSatuan:    harga.IDSatuan,
		NamaSatuan:  harga.NamaSatuan,
		Harga:       harga.Harga,
		BerlakuDari: harga.BerlakuDari.Format(dateOnly),
		CreatedAt:   harga.CreatedAt,
		CreatedBy:   harga.CreatedBy,
	}

	// null means open-ended, not "unknown". Kept as a pointer so the distinction
	// survives into JSON.
	if harga.BerlakuSampai != nil {
		sampai := harga.BerlakuSampai.Format(dateOnly)
		response.BerlakuSampai = &sampai
	}

	return response
}

func ProductHargaJualToResponses(list []entity.ProductHargaJual) []model.ProductHargaJualResponse {
	responses := make([]model.ProductHargaJualResponse, len(list))
	for i := range list {
		responses[i] = *ProductHargaJualToResponse(&list[i])
	}

	return responses
}

func HargaJualBerlakuToResponse(harga *entity.HargaJualBerlaku) *model.HargaJualBerlakuResponse {
	response := &model.HargaJualBerlakuResponse{
		IDHargaJual: harga.IDHargaJual,
		IDSatuan:    harga.IDSatuan,
		NamaSatuan:  harga.NamaSatuan,
		Harga:       harga.Harga,
		BerlakuDari: harga.BerlakuDari.Format(dateOnly),
	}

	if harga.BerlakuSampai != nil {
		sampai := harga.BerlakuSampai.Format(dateOnly)
		response.BerlakuSampai = &sampai
	}

	return response
}

func HargaJualBerlakuToResponses(list []entity.HargaJualBerlaku) []model.HargaJualBerlakuResponse {
	// make, not var: a product with no price in force on the date asked about is
	// exactly the case a client reading data.length would hit first.
	responses := make([]model.HargaJualBerlakuResponse, len(list))
	for i := range list {
		responses[i] = *HargaJualBerlakuToResponse(&list[i])
	}

	return responses
}

func DaftarHargaJualToResponse(row *entity.DaftarHargaJual) *model.DaftarHargaJualResponse {
	response := &model.DaftarHargaJualResponse{
		IDProduct:   row.IDProduct,
		KodeBarang:  row.KodeBarang,
		NamaProduct: row.NamaProduct,
		IsAktif:     row.IsAktif,
		IDSatuan:    row.IDSatuan,
		NamaSatuan:  row.NamaSatuan,
		IDHargaJual: row.IDHargaJual,
		Harga:       row.Harga,
	}

	if row.BerlakuDari != nil {
		dari := row.BerlakuDari.Format(dateOnly)
		response.BerlakuDari = &dari
	}

	if row.BerlakuSampai != nil {
		sampai := row.BerlakuSampai.Format(dateOnly)
		response.BerlakuSampai = &sampai
	}

	return response
}

func DaftarHargaJualToResponses(list []entity.DaftarHargaJual) []model.DaftarHargaJualResponse {
	responses := make([]model.DaftarHargaJualResponse, len(list))
	for i := range list {
		responses[i] = *DaftarHargaJualToResponse(&list[i])
	}

	return responses
}

// PosProductToResponse converts one POS catalog row — isu #11. Satuan is built with
// make, not left nil, so a product row never serialises satuan as null: every
// product carries at least its base unit, but the empty case is guarded here anyway
// rather than trusted, the same discipline ProductToResponses uses for the plain
// product list.
func PosProductToResponse(product *entity.ProductPOS) *model.PosProductResponse {
	return &model.PosProductResponse{
		ID:         product.ID,
		KodeBarang: product.KodeBarang,
		Nama:       product.Nama,
		Satuan:     PosProductSatuanToResponses(product.Satuan),
		StokAkhir:  product.StokAkhir,
	}
}

func PosProductToResponses(list []entity.ProductPOS) []model.PosProductResponse {
	responses := make([]model.PosProductResponse, len(list))
	for i := range list {
		responses[i] = *PosProductToResponse(&list[i])
	}

	return responses
}

func PosProductSatuanToResponse(satuan *entity.ProductPOSSatuan) *model.PosProductSatuanResponse {
	return &model.PosProductSatuanResponse{
		IDSatuan:       satuan.IDSatuan,
		NamaSatuan:     satuan.NamaSatuan,
		Faktor:         satuan.Faktor,
		IsDefaultInput: satuan.IsDefaultInput,
		IDHargaJual:    satuan.IDHargaJual,
		Harga:          satuan.Harga,
	}
}

func PosProductSatuanToResponses(list []entity.ProductPOSSatuan) []model.PosProductSatuanResponse {
	responses := make([]model.PosProductSatuanResponse, len(list))
	for i := range list {
		responses[i] = *PosProductSatuanToResponse(&list[i])
	}

	return responses
}
