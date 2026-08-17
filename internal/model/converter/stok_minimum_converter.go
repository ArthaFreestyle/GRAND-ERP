package converter

import (
	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
)

// StokMinimumToResponses attaches each product's per-room breakdown from a batch read
// (KartuStokRepository.SaldoPerRuangBatch), keyed by IDProduct — the same
// "one batch query, not one per row" shape POS and riwayat-beli already use.
func StokMinimumToResponses(list []entity.StokMinimumBaris, perRuang map[int64][]entity.SaldoStok) []model.StokMinimumResponse {
	// make, not var: a nil slice serialises to null instead of [], and an empty page
	// (nothing below minimum) is exactly the case a client reading data.length hits
	// first.
	responses := make([]model.StokMinimumResponse, len(list))

	for i := range list {
		baris := &list[i]

		responses[i] = model.StokMinimumResponse{
			IDProduct:   baris.IDProduct,
			KodeBarang:  baris.KodeBarang,
			NamaProduct: baris.NamaProduct,
			StokMinimum: baris.StokMinimum,
			TotalStok:   baris.TotalStok,
			Selisih:     baris.StokMinimum - baris.TotalStok,
			PerRuang:    StokRuangToResponses(perRuang[baris.IDProduct]),
		}
	}

	return responses
}
