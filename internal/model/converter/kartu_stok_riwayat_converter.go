package converter

import (
	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
)

func RiwayatKartuStokToResponse(baris *entity.RiwayatKartuStok) *model.KartuStokResponse {
	return &model.KartuStokResponse{
		ID:               baris.ID,
		TanggalTransaksi: baris.TanggalTransaksi,
		JenisTransaksi:   baris.JenisTransaksi,

		StokAwal:   baris.StokAwal,
		StokMasuk:  baris.StokMasuk,
		StokKeluar: baris.StokKeluar,
		StokAkhir:  baris.StokAkhir,

		QtyInput:        baris.QtyInput,
		NamaSatuanInput: baris.NamaSatuanInput,

		HargaPokokSatuan: baris.HargaPokokSatuan,
		NilaiMasuk:       baris.NilaiMasuk,
		NilaiKeluar:      baris.NilaiKeluar,
		NilaiAkhir:       baris.NilaiAkhir,

		RefTable:       baris.RefTable,
		RefIDTransaksi: baris.RefIDTransaksi,
		NomorDokumen:   baris.NomorDokumen,

		IDKartuStokAsal: baris.IDKartuStokAsal,
		Keterangan:      baris.Keterangan,
	}
}

func RiwayatKartuStokToResponses(list []entity.RiwayatKartuStok) []model.KartuStokResponse {
	// make, not var: a nil slice serialises to null instead of [], and an empty page
	// is exactly the case a client reading data.length hits first.
	responses := make([]model.KartuStokResponse, len(list))
	for i := range list {
		responses[i] = *RiwayatKartuStokToResponse(&list[i])
	}

	return responses
}
