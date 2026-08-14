package converter

import (
	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
)

func StokRuangToResponse(saldo *entity.SaldoStok) *model.StokRuangResponse {
	return &model.StokRuangResponse{
		IDRuang:   saldo.IDRuang,
		NamaRuang: saldo.NamaRuang,

		StokAkhir:        saldo.StokAkhir,
		NilaiAkhir:       saldo.NilaiAkhir,
		HargaPokokSatuan: saldo.HargaPokokSatuan,

		TanggalTransaksi:   saldo.TanggalTransaksi,
		TerakhirDiperbarui: saldo.TerakhirDiperbarui,
	}
}

func StokRuangToResponses(list []entity.SaldoStok) []model.StokRuangResponse {
	// make, not var: a nil slice serialises to null instead of [], and a product that
	// has never moved is exactly the case a client reading data.length hits first.
	responses := make([]model.StokRuangResponse, len(list))
	for i := range list {
		responses[i] = *StokRuangToResponse(&list[i])
	}

	return responses
}
