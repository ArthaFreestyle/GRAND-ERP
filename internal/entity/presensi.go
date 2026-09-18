package entity

import "time"

// Shift vocabulary, guarded by presensi_shift_check rather than by a PostgreSQL
// enum — migration 000028 says why: a third shift has to stay cheap to add, and
// ALTER TYPE ... ADD VALUE cannot be undone.
//
// The hours behind the names (PAGI 07:00–17:00, MALAM 18:00–21:00 WIB) are not
// stored anywhere yet, deliberately. Judging lateness needs a per-unit schedule
// master, and until that exists this module records facts and grades nothing.
const (
	ShiftPagi  = "PAGI"
	ShiftMalam = "MALAM"
)

// Status vocabulary. BUKA is a shift with no clock-out yet; SELESAI closed
// normally through the Pulang button or a correction that filled jam_pulang;
// LUPA_PULANG is a shift that closed without one, either because the next tap
// opened a different (tanggal, shift) or because the daily sweep found it still
// open after its date had passed.
const (
	StatusPresensiBuka       = "BUKA"
	StatusPresensiSelesai    = "SELESAI"
	StatusPresensiLupaPulang = "LUPA_PULANG"
)

// Where a column came from. TOMBOL is someone tapping for themselves; KOREKSI is
// someone else typing or moving it afterwards, and a row that carries it must
// stay readable as such forever — that is the whole point of the columns.
// SumberMasuk and SumberPulang are tracked separately because a correction may
// touch only one of the two events.
const (
	SumberPresensiTombol  = "TOMBOL"
	SumberPresensiKoreksi = "KOREKSI"
)

// Presensi maps one shift's attendance: this person clocked in here, at this
// hour, in this shift, and (once closed) clocked out at that hour.
//
// IDUser is never taken from a request body. If a client could send it, one
// employee could mark a colleague present who has not arrived yet, and the
// module loses its reason to exist — so it comes from the verified session and
// the tap DTOs have no such field at all.
//
// There is no automatic clock-out and no invented one: JamPulang stays nil for
// as long as nobody has tapped Pulang, and a row left that way past its own date
// is marked LUPA_PULANG, never given a guessed hour.
//
// IDUnitKerja is a snapshot of the session's active grant at Masuk time,
// nullable because a global grant carries no unit. It is what the read-side
// scoping filters on, so it must never be re-derived later: a grant that moves
// to another unit next month would otherwise rewrite last month's recap.
type Presensi struct {
	ID        int64
	IDUser    int64
	Tanggal   time.Time
	Shift     string
	JamMasuk  time.Time
	JamPulang *time.Time
	Status    string

	IDUnitKerja  *int64
	SumberMasuk  string
	SumberPulang *string
	IPMasuk      *string
	IPPulang     *string

	DikoreksiOleh *int64
	TsKoreksi     *time.Time
	AlasanKoreksi *string

	CreatedAt time.Time
	UpdatedAt time.Time

	// Not columns of presensi. Filled by the read queries through a join on
	// users and unit_kerja — resolving either per row would be an N+1.
	NamaUser      string
	NamaUnitKerja *string
}

// PresensiRekap is one employee's month, and it is the figure this module exists
// to produce: how many days someone completed a shift, split by shift, plus the
// days they completed both.
//
// HariDuaShift is counted separately rather than derived by a client from the
// other two: HariPagiSelesai + HariMalamSelesai counts a two-shift day twice,
// and no client can tell from those two numbers alone which days overlapped.
//
// HariLupaPulangPagi/Malam are tracked apart from the SELESAI counts and never
// folded into them — whether a forgotten clock-out still counts as a day worked
// is a payroll policy this module deliberately does not decide.
//
// It reports and does not decide. Whether a two-shift day is paid at a higher
// rate is a payroll question, and payroll is deliberately out of this module's
// scope — answering it here would lock a pay policy into a recap query before
// the policy itself has been written.
type PresensiRekap struct {
	IDUser               int64
	NamaUser             string
	Tahun                int
	Bulan                int
	HariPagiSelesai      int
	HariMalamSelesai     int
	HariDuaShift         int
	HariLupaPulangPagi   int
	HariLupaPulangMalam  int
	TotalMenitKerjaPagi  int
	TotalMenitKerjaMalam int
}
