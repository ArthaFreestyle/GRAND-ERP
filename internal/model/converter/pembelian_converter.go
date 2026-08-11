package converter

import (
	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
)

func PembelianToResponse(pembelian *entity.Pembelian) *model.PembelianResponse {
	response := &model.PembelianResponse{
		ID:      pembelian.ID,
		Nomor:   pembelian.Nomor,
		Tanggal: pembelian.Tanggal,

		IDSupplier:       pembelian.IDSupplier,
		NamaSupplier:     pembelian.NamaSupplier,
		IDRuang:          pembelian.IDRuang,
		NamaRuang:        pembelian.NamaRuang,
		NoFakturSupplier: pembelian.NoFakturSupplier,

		Subtotal:       pembelian.Subtotal,
		DiskonNota:     pembelian.DiskonNota,
		PPN:            pembelian.PPN,
		PPNDikreditkan: pembelian.PPNDikreditkan,
		Pembulatan:     pembelian.Pembulatan,
		Total:          pembelian.Total,

		IDEkspedisi:         pembelian.IDEkspedisi,
		NoResi:              pembelian.NoResi,
		TotalKoli:           pembelian.TotalKoli,
		TarifPerKoli:        pembelian.TarifPerKoli,
		BiayaAngkut:         pembelian.BiayaAngkut,
		DitanggungSupplier:  pembelian.DitanggungSupplier,
		MetodeAlokasiAngkut: pembelian.MetodeAlokasiAngkut,

		JenisPembayaran:  pembelian.JenisPembayaran,
		StatusPembayaran: pembelian.StatusPembayaran,
		StatusPenerimaan: pembelian.StatusPenerimaan,
		Status:           pembelian.Status,

		CreatedBy: pembelian.CreatedBy,
		CreatedAt: pembelian.CreatedAt,

		DiajukanOleh:  pembelian.DiajukanOleh,
		DiajukanPada:  pembelian.DiajukanPada,
		DisetujuiOleh: pembelian.DisetujuiOleh,
		DisetujuiPada: pembelian.DisetujuiPada,
		PostedAt:      pembelian.PostedAt,

		DibatalkanOleh: pembelian.DibatalkanOleh,
		AlasanBatal:    pembelian.AlasanBatal,
		AlasanTolak:    pembelian.AlasanTolak,
	}

	// tanggal_faktur is a DATE: no time and no zone, so rendering it as an RFC 3339
	// timestamp would invent both.
	if pembelian.TanggalFaktur != nil {
		tanggal := pembelian.TanggalFaktur.Format(dateOnly)
		response.TanggalFaktur = &tanggal
	}

	// Left nil on list reads, where the lines are not fetched, so `omitempty` drops
	// the key rather than claiming the document has none.
	if pembelian.Detail != nil {
		response.Detail = PembelianDetailToResponses(pembelian.Detail)
	}

	return response
}

func PembelianToResponses(list []entity.Pembelian) []model.PembelianResponse {
	// make, not var: a nil slice serialises to null instead of [].
	responses := make([]model.PembelianResponse, len(list))
	for i := range list {
		responses[i] = *PembelianToResponse(&list[i])
	}

	return responses
}

func PembelianDetailToResponse(detail *entity.PembelianDetail) *model.PembelianDetailResponse {
	return &model.PembelianDetailResponse{
		ID:          detail.ID,
		IDProduct:   detail.IDProduct,
		KodeBarang:  detail.KodeBarang,
		NamaProduct: detail.NamaProduct,

		QtyFaktur:        detail.QtyFaktur,
		QtyDiterima:      detail.QtyDiterima,
		IDSatuanInput:    detail.IDSatuanInput,
		NamaSatuan:       detail.NamaSatuan,
		FaktorKonversi:   detail.FaktorKonversi,
		QtyDasar:         detail.QtyDasar,
		QtyDiterimaDasar: detail.QtyDiterimaDasar,
		// Computed here rather than left to the client: the whole receiving screen
		// is built around this number, and every caller would otherwise derive it
		// the same way.
		SelisihDasar:    detail.QtyDasar - detail.QtyDiterimaDasar,
		NamaSatuanDasar: detail.NamaSatuanDasar,

		HargaSatuanInput:      detail.HargaSatuanInput,
		DiskonBaris:           detail.DiskonBaris,
		Subtotal:              detail.Subtotal,
		JumlahKoli:            detail.JumlahKoli,
		AlokasiBiaya:          detail.AlokasiBiaya,
		HargaPokokSatuanDasar: detail.HargaPokokSatuanDasar,
		KeteranganSelisih:     detail.KeteranganSelisih,
	}
}

func PembelianDetailToResponses(list []entity.PembelianDetail) []model.PembelianDetailResponse {
	responses := make([]model.PembelianDetailResponse, len(list))
	for i := range list {
		responses[i] = *PembelianDetailToResponse(&list[i])
	}

	return responses
}

// PembelianToSisaResponse projects a document down to the lines that are still
// short. Lines delivered in full are dropped: this is a to-chase list, and a
// complete line is not on it.
func PembelianToSisaResponse(pembelian *entity.Pembelian) *model.SisaPembelianResponse {
	response := &model.SisaPembelianResponse{
		ID:               pembelian.ID,
		Nomor:            pembelian.Nomor,
		IDSupplier:       pembelian.IDSupplier,
		NamaSupplier:     pembelian.NamaSupplier,
		Status:           pembelian.Status,
		StatusPenerimaan: pembelian.StatusPenerimaan,
		Baris:            make([]model.SisaPembelianBarisRow, 0, len(pembelian.Detail)),
	}

	for i := range pembelian.Detail {
		detail := &pembelian.Detail[i]

		sisa := detail.QtyDasar - detail.QtyDiterimaDasar
		if sisa <= 0 {
			continue
		}

		response.Baris = append(response.Baris, model.SisaPembelianBarisRow{
			IDPembelianDetail: detail.ID,
			IDProduct:         detail.IDProduct,
			KodeBarang:        detail.KodeBarang,
			NamaProduct:       detail.NamaProduct,
			QtyDasar:          detail.QtyDasar,
			QtyDiterimaDasar:  detail.QtyDiterimaDasar,
			SisaDasar:         sisa,
			NamaSatuanDasar:   detail.NamaSatuanDasar,
			KeteranganSelisih: detail.KeteranganSelisih,
		})
	}

	return response
}
