package converter

import (
	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
)

// DokumenToResponse maps one attachment. path_simpan is deliberately absent from
// the response: it is where the file sits on the server's disk, and a client that
// knows it gains nothing it cannot get from GET /dokumen/{id} — while a client that
// guesses at it is the beginning of a directory traversal.
func DokumenToResponse(dokumen *entity.Dokumen) *model.DokumenResponse {
	return &model.DokumenResponse{
		ID:             dokumen.ID,
		NamaAsli:       dokumen.NamaAsli,
		Mime:           dokumen.Mime,
		UkuranByte:     dokumen.UkuranByte,
		ChecksumSHA256: dokumen.ChecksumSHA256,
		RefTable:       dokumen.RefTable,
		RefID:          dokumen.RefID,
		CreatedBy:      dokumen.CreatedBy,
		NamaPembuat:    dokumen.NamaPembuat,
		CreatedAt:      dokumen.CreatedAt,
		DeletedAt:      dokumen.DeletedAt,
	}
}

func DokumenToResponses(list []entity.Dokumen) []model.DokumenResponse {
	// make, not var: a nil slice serialises to null instead of [].
	responses := make([]model.DokumenResponse, len(list))
	for i := range list {
		responses[i] = *DokumenToResponse(&list[i])
	}

	return responses
}
