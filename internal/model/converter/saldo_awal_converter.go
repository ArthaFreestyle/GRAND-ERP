package converter

import (
	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
)

func SaldoAwalToResponse(saldoAwal *entity.SaldoAwal) *model.SaldoAwalResponse {
	response := &model.SaldoAwalResponse{
		ID:        saldoAwal.ID,
		Nomor:     saldoAwal.Nomor,
		Tanggal:   saldoAwal.Tanggal,
		IDRuang:   saldoAwal.IDRuang,
		NamaRuang: saldoAwal.NamaRuang,
		Alasan:    saldoAwal.Alasan,
		Status:    saldoAwal.Status,

		TotalNilai: saldoAwal.TotalNilai,

		CreatedBy:     saldoAwal.CreatedBy,
		CreatedAt:     saldoAwal.CreatedAt,
		DiajukanOleh:  saldoAwal.DiajukanOleh,
		DiajukanPada:  saldoAwal.DiajukanPada,
		DisetujuiOleh: saldoAwal.DisetujuiOleh,
		DisetujuiPada: saldoAwal.DisetujuiPada,
		PostedAt:      saldoAwal.PostedAt,

		DibatalkanOleh: saldoAwal.DibatalkanOleh,
		AlasanBatal:    saldoAwal.AlasanBatal,
		AlasanTolak:    saldoAwal.AlasanTolak,
	}

	// Left nil on list reads, where the lines are not fetched, so `omitempty` drops the
	// key rather than claiming the document has none.
	if saldoAwal.Detail != nil {
		response.Detail = SaldoAwalDetailToResponses(saldoAwal.Detail)
	}

	return response
}

func SaldoAwalToResponses(list []entity.SaldoAwal) []model.SaldoAwalResponse {
	// make, not var: a nil slice serialises to null instead of [].
	responses := make([]model.SaldoAwalResponse, len(list))
	for i := range list {
		responses[i] = *SaldoAwalToResponse(&list[i])
	}

	return responses
}

func SaldoAwalDetailToResponse(detail *entity.SaldoAwalDetail) *model.SaldoAwalDetailResponse {
	return &model.SaldoAwalDetailResponse{
		ID:          detail.ID,
		IDProduct:   detail.IDProduct,
		KodeBarang:  detail.KodeBarang,
		NamaProduct: detail.NamaProduct,

		QtyInput:        detail.QtyInput,
		IDSatuanInput:   detail.IDSatuanInput,
		NamaSatuan:      detail.NamaSatuan,
		FaktorKonversi:  detail.FaktorKonversi,
		QtyDasar:        detail.QtyDasar,
		NamaSatuanDasar: detail.NamaSatuanDasar,

		HargaSatuanInput:      detail.HargaSatuanInput,
		HargaPokokSatuanDasar: detail.HargaPokokSatuanDasar,
		NilaiMasuk:            detail.NilaiMasuk,
		IDKartuStok:           detail.IDKartuStok,
	}
}

func SaldoAwalDetailToResponses(list []entity.SaldoAwalDetail) []model.SaldoAwalDetailResponse {
	responses := make([]model.SaldoAwalDetailResponse, len(list))
	for i := range list {
		responses[i] = *SaldoAwalDetailToResponse(&list[i])
	}

	return responses
}
