package converter

import (
	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
)

func NilaiPersediaanToResponses(list []entity.NilaiPersediaanBaris) []model.NilaiPersediaanResponse {
	responses := make([]model.NilaiPersediaanResponse, len(list))
	for i := range list {
		responses[i] = model.NilaiPersediaanResponse{
			IDRuang:    list[i].IDRuang,
			NamaRuang:  list[i].NamaRuang,
			TotalNilai: list[i].TotalNilai,
		}
	}

	return responses
}

func LabaKotorToResponses(list []entity.LabaKotorBaris) []model.LabaKotorResponse {
	responses := make([]model.LabaKotorResponse, len(list))
	for i := range list {
		responses[i] = model.LabaKotorResponse{
			Bulan:          list[i].Bulan,
			TotalPenjualan: list[i].TotalPenjualan,
			TotalHPP:       list[i].TotalHPP,
			LabaKotor:      list[i].LabaKotor,
		}
	}

	return responses
}

func PergerakanToResponses(list []entity.PergerakanBaris) []model.PergerakanResponse {
	responses := make([]model.PergerakanResponse, len(list))
	for i := range list {
		responses[i] = model.PergerakanResponse{
			IDProduct:      list[i].IDProduct,
			KodeBarang:     list[i].KodeBarang,
			NamaProduct:    list[i].NamaProduct,
			IDRuang:        list[i].IDRuang,
			NamaRuang:      list[i].NamaRuang,
			JenisTransaksi: list[i].JenisTransaksi,
			TotalMasuk:     list[i].TotalMasuk,
			TotalKeluar:    list[i].TotalKeluar,
		}
	}

	return responses
}
