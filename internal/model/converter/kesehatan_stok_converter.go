package converter

import (
	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
)

// KesehatanStokRuangToResponse maps one room's read row plus the two scores the
// usecase derived from it. The scores are passed in rather than computed here: the
// arithmetic lives in the usecase, where it is tested without a database.
func KesehatanStokRuangToResponse(
	baris entity.KesehatanStokRuang, skorStokMati *int64, skorAkurasiOpname int64,
) model.KesehatanStokRuangResponse {
	return model.KesehatanStokRuangResponse{
		IDRuang:            baris.IDRuang,
		NamaRuang:          baris.NamaRuang,
		TotalStok:          baris.TotalStok,
		NilaiPersediaan:    baris.NilaiPersediaan,
		NilaiStokMati:      baris.NilaiStokMati,
		SkorStokMati:       skorStokMati,
		NomorOpname:        baris.NomorOpname,
		TsCutoffOpname:     baris.TsCutoffOpname,
		NilaiSelisihOpname: baris.NilaiSelisihOpname,
		SkorAkurasiOpname:  skorAkurasiOpname,
	}
}
