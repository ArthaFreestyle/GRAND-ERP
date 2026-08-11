package converter

import (
	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
)

func RiwayatBeliToResponse(riwayat *entity.RiwayatBeli) *model.RiwayatBeliResponse {
	return &model.RiwayatBeliResponse{
		IDSupplier:   riwayat.IDSupplier,
		NamaSupplier: riwayat.NamaSupplier,

		IDPembelian:      riwayat.IDPembelian,
		Nomor:            riwayat.Nomor,
		Tanggal:          riwayat.Tanggal,
		NoFakturSupplier: riwayat.NoFakturSupplier,

		IDPembelianDetail: riwayat.IDPembelianDetail,
		QtyFaktur:         riwayat.QtyFaktur,
		IDSatuanInput:     riwayat.IDSatuanInput,
		NamaSatuan:        riwayat.NamaSatuan,
		FaktorKonversi:    riwayat.FaktorKonversi,
		QtyDasar:          riwayat.QtyDasar,
		NamaSatuanDasar:   riwayat.NamaSatuanDasar,

		HargaSatuanInput:      riwayat.HargaSatuanInput,
		DiskonBaris:           riwayat.DiskonBaris,
		Subtotal:              riwayat.Subtotal,
		AlokasiBiaya:          riwayat.AlokasiBiaya,
		HargaSatuanDasar:      riwayat.HargaSatuanDasar,
		HargaPokokSatuanDasar: riwayat.HargaPokokSatuanDasar,
	}
}

func RiwayatBeliToResponses(list []entity.RiwayatBeli) []model.RiwayatBeliResponse {
	// make, not var: a nil slice serialises to null instead of [], and a product
	// nobody has ever bought is exactly the case a client reading data.length would
	// hit first.
	responses := make([]model.RiwayatBeliResponse, len(list))
	for i := range list {
		responses[i] = *RiwayatBeliToResponse(&list[i])
	}

	return responses
}
