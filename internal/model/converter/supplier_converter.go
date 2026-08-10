package converter

import (
	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
)

func SupplierToResponse(supplier *entity.Supplier) *model.SupplierResponse {
	return &model.SupplierResponse{
		ID:          supplier.ID,
		Kode:        supplier.Kode,
		Nama:        supplier.Nama,
		Telepon:     supplier.Telepon,
		Alamat:      supplier.Alamat,
		NPWP:        supplier.NPWP,
		IsAktif:     supplier.IsAktif,
		CreatedAt:   supplier.CreatedAt,
		CreatedBy:   supplier.CreatedBy,
		NamaPembuat: supplier.NamaPembuat,
		UpdatedAt:   supplier.UpdatedAt,
		UpdatedBy:   supplier.UpdatedBy,
	}
}

func SupplierToResponses(list []entity.Supplier) []model.SupplierResponse {
	// make, not var: a nil slice serialises to null instead of [].
	responses := make([]model.SupplierResponse, len(list))
	for i := range list {
		responses[i] = *SupplierToResponse(&list[i])
	}

	return responses
}
