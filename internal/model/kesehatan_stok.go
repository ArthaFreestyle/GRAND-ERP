package model

import "time"

// Isu #37: one 0-100 figure for the stock health of the caller's active unit_kerja,
// never returned without the numbers that produced it.

// Component codes and status labels of GET /laporan/kesehatan-stok.
const (
	KomponenKetersediaan   = "KETERSEDIAAN"
	KomponenStokMati       = "STOK_MATI"
	KomponenAkurasiOpname  = "AKURASI_OPNAME"
	KomponenCakupanMinimum = "CAKUPAN_MINIMUM"

	StatusKesehatanSehat          = "SEHAT"
	StatusKesehatanPerluPerhatian = "PERLU_PERHATIAN"
	StatusKesehatanKritis         = "KRITIS"
)

// KesehatanStokRequest asks for the health score of the caller's active unit_kerja.
//
// IDRuang, given, narrows it to one room. A room outside the active unit is not an
// error: the scope simply holds nothing, and the answer is skor null, the same way a
// list-shaped read silently omits rows outside the unit (isu #12 fase 6).
type KesehatanStokRequest struct {
	IDRuang *int64 `query:"id_ruang" validate:"omitempty,gt=0"`

	// AktifIDUnitKerja is filled from the session's active grant by the controller,
	// never from the request. Nil (a global grant) scores the whole company.
	AktifIDUnitKerja *int64 `query:"-"`
}

// KesehatanStokResponse is the score, its status, and every component behind it.
//
// Skor and Status are null when no component applies at all — a scope that has
// never held stock is neither healthy (100) nor sick (0). Komponen always carries
// all four entries, each with its own nullable Skor, so a client can render the
// same layout whatever the data.
type KesehatanStokResponse struct {
	IDUnitKerja  *int64                          `json:"id_unit_kerja"`
	IDRuang      *int64                          `json:"id_ruang"`
	Skor         *int64                          `json:"skor"`
	Status       *string                         `json:"status"`
	DihitungPada time.Time                       `json:"dihitung_pada"`
	Komponen     []KesehatanStokKomponenResponse `json:"komponen"`
	Ruang        []KesehatanStokRuangResponse    `json:"ruang"`
}

// KesehatanStokKomponenResponse is one component. Rincian's shape depends on Kode:
// KetersediaanRincian, StokMatiRincian, AkurasiOpnameRincian, or
// CakupanMinimumRincian.
type KesehatanStokKomponenResponse struct {
	Kode    string `json:"kode"`
	Bobot   int64  `json:"bobot"`
	Skor    *int64 `json:"skor"`
	Rincian any    `json:"rincian"`
}

// KetersediaanRincian: of ProdukDinilai products with a minimum that the scope
// carries, how many sit above it, at or below it, and at zero.
type KetersediaanRincian struct {
	ProdukDinilai int64 `json:"produk_dinilai"`
	Sehat         int64 `json:"sehat"`
	Menipis       int64 `json:"menipis"`
	Habis         int64 `json:"habis"`
}

// StokMatiRincian: how much of the stock value in scope has seen no sale or usage
// in the unit for HariAmbang days.
type StokMatiRincian struct {
	NilaiPersediaan string `json:"nilai_persediaan"`
	NilaiStokMati   string `json:"nilai_stok_mati"`
	HariAmbang      int    `json:"hari_ambang"`
}

// AkurasiOpnameRincian: of RuangDinilai rooms holding stock, how many have no POSTED
// opname within HariAmbang days, and the summed selisih and counted value of the
// ones that do.
type AkurasiOpnameRincian struct {
	RuangDinilai     int64  `json:"ruang_dinilai"`
	RuangTanpaOpname int64  `json:"ruang_tanpa_opname"`
	NilaiSelisih     string `json:"nilai_selisih"`
	NilaiDihitung    string `json:"nilai_dihitung"`
	HariAmbang       int    `json:"hari_ambang"`
}

// CakupanMinimumRincian: of ProdukDipegang products in stock, how many have a
// stok_minimum configured at all.
type CakupanMinimumRincian struct {
	ProdukDipegang   int64 `json:"produk_dipegang"`
	ProdukBerminimum int64 `json:"produk_berminimum"`
}

// KesehatanStokRuangResponse breaks the two room-shaped components down per room
// holding stock. KETERSEDIAAN and CAKUPAN_MINIMUM are not broken down: stok_minimum
// is a per-product number, and comparing it per room would flag a warehouse kept
// deliberately empty because its stock lives in the shop.
//
// SkorStokMati is null only for a room whose stock carries no value at all.
// SkorAkurasiOpname is 0, not null, for a room with no opname in the window —
// NomorOpname null says why.
type KesehatanStokRuangResponse struct {
	IDRuang            int64      `json:"id_ruang"`
	NamaRuang          string     `json:"nama_ruang"`
	TotalStok          int64      `json:"total_stok"`
	NilaiPersediaan    string     `json:"nilai_persediaan"`
	NilaiStokMati      string     `json:"nilai_stok_mati"`
	SkorStokMati       *int64     `json:"skor_stok_mati"`
	NomorOpname        *string    `json:"nomor_opname"`
	TsCutoffOpname     *time.Time `json:"ts_cutoff_opname"`
	NilaiSelisihOpname *string    `json:"nilai_selisih_opname"`
	SkorAkurasiOpname  int64      `json:"skor_akurasi_opname"`
}
