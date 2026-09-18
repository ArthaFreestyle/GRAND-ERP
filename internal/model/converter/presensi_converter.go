package converter

import (
	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
)

// durasiKerjaMenit computes minutes worked at read time — never stored. A shift
// still open (JamPulang nil) answers nil rather than 0: zero would read as
// "clocked out the same instant", nil as "not answerable yet". Same distinction
// stok_opname draws between stok_so = NULL and stok_so = 0.
func durasiKerjaMenit(jamMasuk *entity.Presensi) *int {
	if jamMasuk.JamPulang == nil {
		return nil
	}

	menit := int(jamMasuk.JamPulang.Sub(jamMasuk.JamMasuk).Minutes())

	return &menit
}

func PresensiToResponse(presensi *entity.Presensi) *model.PresensiResponse {
	return &model.PresensiResponse{
		ID:               presensi.ID,
		IDUser:           presensi.IDUser,
		NamaUser:         presensi.NamaUser,
		Tanggal:          presensi.Tanggal,
		Shift:            presensi.Shift,
		JamMasuk:         presensi.JamMasuk,
		JamPulang:        presensi.JamPulang,
		Status:           presensi.Status,
		DurasiKerjaMenit: durasiKerjaMenit(presensi),
		IDUnitKerja:      presensi.IDUnitKerja,
		NamaUnitKerja:    presensi.NamaUnitKerja,
		SumberMasuk:      presensi.SumberMasuk,
		SumberPulang:     presensi.SumberPulang,
		IPMasuk:          presensi.IPMasuk,
		IPPulang:         presensi.IPPulang,
		DikoreksiOleh:    presensi.DikoreksiOleh,
		TsKoreksi:        presensi.TsKoreksi,
		AlasanKoreksi:    presensi.AlasanKoreksi,
		CreatedAt:        presensi.CreatedAt,
		UpdatedAt:        presensi.UpdatedAt,
	}
}

func PresensiToResponses(list []entity.Presensi) []model.PresensiResponse {
	// make, not var: a nil slice serialises to null instead of [], and a page
	// with no rows is exactly the one a client reading data.length hits
	// first.
	responses := make([]model.PresensiResponse, len(list))
	for i := range list {
		responses[i] = *PresensiToResponse(&list[i])
	}

	return responses
}

func RekapPresensiToResponse(rekap *entity.PresensiRekap) *model.RekapPresensiResponse {
	return &model.RekapPresensiResponse{
		IDUser:               rekap.IDUser,
		NamaUser:             rekap.NamaUser,
		Tahun:                rekap.Tahun,
		Bulan:                rekap.Bulan,
		HariPagiSelesai:      rekap.HariPagiSelesai,
		HariMalamSelesai:     rekap.HariMalamSelesai,
		HariDuaShift:         rekap.HariDuaShift,
		HariLupaPulangPagi:   rekap.HariLupaPulangPagi,
		HariLupaPulangMalam:  rekap.HariLupaPulangMalam,
		TotalMenitKerjaPagi:  rekap.TotalMenitKerjaPagi,
		TotalMenitKerjaMalam: rekap.TotalMenitKerjaMalam,
	}
}

func RekapPresensiToResponses(list []entity.PresensiRekap) []model.RekapPresensiResponse {
	responses := make([]model.RekapPresensiResponse, len(list))
	for i := range list {
		responses[i] = *RekapPresensiToResponse(&list[i])
	}

	return responses
}
