package model

import "time"

// PresensiResponse is one shift's attendance row as a client sees it.
//
// SumberMasuk/SumberPulang and the three koreksi fields are what keep a typed-in
// or moved row distinguishable from a tapped one forever. A client that shows
// attendance to the person it belongs to should be able to say "this hour was
// entered by someone else, and here is why".
//
// DurasiKerjaMenit is computed at read time, never stored — the one derived
// figure in this project that deliberately is not a snapshot, because both of
// its operands (jam_masuk, jam_pulang) live on the same row a correction can
// still move. It is nil, not zero, for a shift that has not closed: zero would
// read as "clocked in and out at the same instant" rather than "not answerable
// yet".
type PresensiResponse struct {
	ID        int64      `json:"id"`
	IDUser    int64      `json:"id_user"`
	NamaUser  string     `json:"nama_user"`
	Tanggal   time.Time  `json:"tanggal"`
	Shift     string     `json:"shift"`
	JamMasuk  time.Time  `json:"jam_masuk"`
	JamPulang *time.Time `json:"jam_pulang"`
	Status    string     `json:"status"`

	DurasiKerjaMenit *int `json:"durasi_kerja_menit"`

	IDUnitKerja   *int64  `json:"id_unit_kerja"`
	NamaUnitKerja *string `json:"nama_unit_kerja"`
	SumberMasuk   string  `json:"sumber_masuk"`
	SumberPulang  *string `json:"sumber_pulang"`
	IPMasuk       *string `json:"ip_masuk,omitempty"`
	IPPulang      *string `json:"ip_pulang,omitempty"`

	DikoreksiOleh *int64     `json:"dikoreksi_oleh,omitempty"`
	TsKoreksi     *time.Time `json:"ts_koreksi,omitempty"`
	AlasanKoreksi *string    `json:"alasan_koreksi,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// HadirRequest carries no client input at all — that is the point, not an
// oversight.
//
// Every field here is filled by the controller from the verified session or
// from the server's own clock. If id_user could arrive in a body, one employee
// could mark a colleague present who has not reached the office, and the module
// loses its reason to exist. The shift is not here either: it is inferred from
// the tap time, so one button stays one button and nobody can claim a night
// shift while tapping at eight in the morning.
type HadirRequest struct {
	IDUser int64 `json:"-" validate:"required,gt=0"`
	// AktifIDUnitKerja is the session's active unit, snapshotted onto the row.
	// Nil is a legitimate answer — a global grant, or a caller holding several
	// grants who has not switched context yet. Such a caller may still tap; the
	// row simply carries no unit, which is the same reading NULL has everywhere
	// else in this codebase.
	AktifIDUnitKerja *int64 `json:"-"`
	// IPMasuk is ctx.IP() as-is. It proves nothing today — there is no
	// trusted-proxy configuration anywhere in this project, the same caveat
	// login throttling already carries — and is recorded so it becomes useful
	// the day there is one.
	IPMasuk string `json:"-"`
}

// PulangRequest is the mirror of HadirRequest, and just as body-free: closing
// a shift is not something anyone else may do on a caller's behalf either.
type PulangRequest struct {
	IDUser   int64  `json:"-" validate:"required,gt=0"`
	IPPulang string `json:"-"`
}

// GetPresensiHariIniRequest asks what the button should look like right now.
type GetPresensiHariIniRequest struct {
	IDUser int64 `json:"-" validate:"required,gt=0"`
}

// PresensiShiftHariIniResponse is one shift's state today.
//
// Status is BELUM_MASUK for a shift with no row at all — a synthetic answer,
// not a 404, the same shape periode gives a month nobody has closed — or
// otherwise the row's own BUKA/SELESAI/LUPA_PULANG.
type PresensiShiftHariIniResponse struct {
	Shift  string `json:"shift"`
	Status string `json:"status"`

	ID               *int64     `json:"id,omitempty"`
	JamMasuk         *time.Time `json:"jam_masuk,omitempty"`
	JamPulang        *time.Time `json:"jam_pulang,omitempty"`
	DurasiKerjaMenit *int       `json:"durasi_kerja_menit,omitempty"`
}

// PresensiHariIniResponse answers for BOTH shifts at once, not just the one
// running now.
//
// The server decides which button to draw, never the phone: a client inferring
// the shift from its own clock lands people in the wrong shift whenever that
// clock drifts, and around 17:30 a few minutes is all it takes. Sending both
// shifts in one answer is what lets a screen say "pagi sudah selesai, malam
// belum mulai" without a second call — which is exactly what the person about
// to work a second shift is looking at.
type PresensiHariIniResponse struct {
	Tanggal       time.Time                    `json:"tanggal"`
	ShiftSekarang string                       `json:"shift_sekarang"`
	Pagi          PresensiShiftHariIniResponse `json:"pagi"`
	Malam         PresensiShiftHariIniResponse `json:"malam"`
}

// RekapPresensiResponse is one employee's month — the figure payroll is
// waiting for, reported without deciding anything.
//
// HariDuaShift is its own number rather than something a client derives:
// hari_pagi_selesai + hari_malam_selesai counts a two-shift day twice, and
// those two numbers alone cannot say which days overlapped. HariLupaPulang*
// never folds into the SELESAI counts — whether a forgotten clock-out still
// counts as a day worked is a payroll policy this module deliberately leaves
// unwritten.
type RekapPresensiResponse struct {
	IDUser           int64  `json:"id_user"`
	NamaUser         string `json:"nama_user"`
	Tahun            int    `json:"tahun"`
	Bulan            int    `json:"bulan"`
	HariPagiSelesai  int    `json:"hari_pagi_selesai"`
	HariMalamSelesai int    `json:"hari_malam_selesai"`
	HariDuaShift     int    `json:"hari_dua_shift"`

	HariLupaPulangPagi  int `json:"hari_lupa_pulang_pagi"`
	HariLupaPulangMalam int `json:"hari_lupa_pulang_malam"`

	TotalMenitKerjaPagi  int `json:"total_menit_kerja_pagi"`
	TotalMenitKerjaMalam int `json:"total_menit_kerja_malam"`
}

// ListPresensiRequest pages over attendance rows. Used both for
// GET /presensi (SUPERADMIN, every employee, scoped to the active unit) and
// GET /presensi/saya (any caller, forced to their own id_user, never scoped by
// unit) — the controller decides which fields it overwrites after binding.
type ListPresensiRequest struct {
	PageRequest
	IDUser        *int64  `query:"id_user" validate:"omitempty,gt=0"`
	TanggalDari   *string `query:"tanggal_dari" validate:"omitempty,datetime=2006-01-02"`
	TanggalSampai *string `query:"tanggal_sampai" validate:"omitempty,datetime=2006-01-02"`
	Shift         *string `query:"shift" validate:"omitempty,oneof=PAGI MALAM"`
	Status        *string `query:"status" validate:"omitempty,oneof=BUKA SELESAI LUPA_PULANG"`

	// AktifIDUnitKerja carries query:"-" and is overwritten by the controller
	// after binding regardless — the same way ActorID is protected, so a
	// caller cannot widen their own scope through the query string. It is nil
	// for GET /presensi/saya unconditionally: your own history is yours,
	// including the days you worked at another unit.
	AktifIDUnitKerja *int64 `query:"-"`
}

// ListRekapPresensiRequest pages over the monthly recap, one row per employee
// — SUPERADMIN only.
type ListRekapPresensiRequest struct {
	PageRequest
	Tahun int `query:"tahun" validate:"required,min=2000,max=2200"`
	Bulan int `query:"bulan" validate:"required,min=1,max=12"`

	AktifIDUnitKerja *int64 `query:"-"`
}

// UpdatePresensiRequest corrects a row and leaves a trail doing it.
//
// id_user and tanggal are absent on purpose: moving an attendance to another
// person or another day is not a correction. Shift may be corrected — the
// server's inference can miss for someone arriving far outside the usual hours
// — but it is never re-inferred from a corrected JamMasuk. Fixing a one-minute
// typo must not quietly move someone into the other shift; the two are separate
// decisions.
//
// JamPulang may be filled in or moved, but a patch may never null it back out:
// reopening a shift that already closed would reactivate
// presensi_terbuka_uidx for a day already gone, and the person could not clock
// in again tomorrow.
//
// AlasanKoreksi is required by the usecase and may not be cleared. It is the
// only record of why someone's hour changed — the policy-not-constraint
// precedent keterangan_selisih and retur_pembelian.alasan already set.
type UpdatePresensiRequest struct {
	ID      int64 `json:"-" validate:"required,gt=0"`
	ActorID int64 `json:"-" validate:"required,gt=0"`

	// JamMasuk/JamPulang are wall-clock times in WIB ("08:03"), applied to the
	// row's OWN tanggal. That is what keeps a correction on the day it
	// belongs to without a separate rule saying so — and tanggal is not
	// correctable anyway.
	JamMasuk      Optional[string] `json:"jam_masuk" validate:"omitempty,datetime=15:04"`
	JamPulang     Optional[string] `json:"jam_pulang" validate:"omitempty,datetime=15:04"`
	Shift         Optional[string] `json:"shift" validate:"omitempty,oneof=PAGI MALAM"`
	AlasanKoreksi string           `json:"alasan_koreksi" validate:"required,max=1000"`
}
