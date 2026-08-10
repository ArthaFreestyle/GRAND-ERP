package converter

import (
	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
)

func PelangganToResponse(pelanggan *entity.Pelanggan) *model.PelangganResponse {
	return &model.PelangganResponse{
		ID:      pelanggan.ID,
		Kode:    pelanggan.Kode,
		Nama:    pelanggan.Nama,
		Telepon: pelanggan.Telepon,
		Alamat:  pelanggan.Alamat,
		NPWP:    pelanggan.NPWP,
		// Passed through as-is: nil means no credit limit, and coercing it to "0"
		// here would quietly forbid the customer from owing anything.
		PlafonKredit: pelanggan.PlafonKredit,
		IsAktif:      pelanggan.IsAktif,
		CreatedAt:    pelanggan.CreatedAt,
		CreatedBy:    pelanggan.CreatedBy,
		NamaPembuat:  pelanggan.NamaPembuat,
		UpdatedAt:    pelanggan.UpdatedAt,
		UpdatedBy:    pelanggan.UpdatedBy,
	}
}

func PelangganToResponses(list []entity.Pelanggan) []model.PelangganResponse {
	// make, not var: a nil slice serialises to null instead of [].
	responses := make([]model.PelangganResponse, len(list))
	for i := range list {
		responses[i] = *PelangganToResponse(&list[i])
	}

	return responses
}
