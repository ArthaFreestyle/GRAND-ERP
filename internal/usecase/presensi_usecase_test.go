package usecase_test

// isu #40: presensi karyawan. One button in, one button out, two shifts a day,
// and corrections that leave a trail.
//
// What these pin is what the module would lose most quietly: a double tap
// creating a second row, a two-shift day being refused by a unique index keyed
// one column short, a forgotten clock-out inventing an hour instead of honestly
// saying LUPA_PULANG, and a typed-in correction becoming indistinguishable from
// a tapped one.
//
// Masuk/Pulang/HariIni all read PresensiUseCase.Now rather than time.Now
// directly. That seam is what lets these tests stand in for "the next day" or
// "just past midnight" deterministically — the server's clock is still the only
// thing ever consulted, the tests just get to choose what it says.

import (
	"testing"
	"time"

	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
)

// presensiFixture seeds one employee and the unit they work at. Attendance
// needs neither goods nor rooms nor documents, which is most of why this
// module exists as its own slice — the fixture stops at a user and a
// unit_kerja.
type presensiFixture struct {
	actor     int64
	karyawan  int64
	unitKerja int64
}

func newPresensiFixture(t *testing.T, testApp *app) presensiFixture {
	t.Helper()

	bootstrap := testActor(t)

	unit, err := testApp.unitKerja.Create(ctx(), &model.CreateUnitKerjaRequest{
		ActorID: bootstrap, Kode: ptr("PRS"), Nama: "Unit Presensi",
	})
	if err != nil {
		t.Fatalf("create unit kerja: %v", err)
	}

	karyawan, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		ActorID: bootstrap, Username: "karyawan_presensi", Password: "rahasia123",
		NamaLengkap: ptr("Karyawan Presensi"),
	})
	if err != nil {
		t.Fatalf("create karyawan: %v", err)
	}

	return presensiFixture{actor: bootstrap, karyawan: karyawan.ID, unitKerja: unit.ID}
}

// zonaUjiWIB mirrors usecase.zonaWIB (unexported, so this package cannot see
// it) — a fixed UTC+7 offset, never time.LoadLocation, for the same reason
// stated at that definition: no DST in western Indonesia, no tzdata dependency.
var zonaUjiWIB = time.FixedZone("WIB", 7*60*60)

// jamUji builds a wall-clock moment in WIB, the only calendar this module
// reasons in.
func jamUji(tahun int, bulan time.Month, hari, jam, menit int) time.Time {
	return time.Date(tahun, bulan, hari, jam, menit, 0, 0, zonaUjiWIB)
}

// tanggalUji is a fixed date the deterministic-clock tests work on, so a test
// run near midnight cannot land its rows in two different months and make the
// recap disagree with itself.
const tanggalUji = "2026-03-17"

// masukPada points the usecase's clock at jam and taps Masuk for the fixture's
// employee. Every test that needs a specific shift on a specific date goes
// through here rather than through a real clock, which a test has no business
// waiting on.
func masukPada(t *testing.T, testApp *app, f presensiFixture, jam time.Time, aktifUnit *int64) *model.PresensiResponse {
	t.Helper()

	testApp.presensi.Now = func() time.Time { return jam }

	response, err := testApp.presensi.Masuk(ctx(), &model.HadirRequest{
		IDUser: f.karyawan, AktifIDUnitKerja: aktifUnit,
	})
	if err != nil {
		t.Fatalf("masuk %s: %v", jam.Format(time.RFC3339), err)
	}

	return response
}

// pulangPada is masukPada's mirror for the clock-out button.
func pulangPada(t *testing.T, testApp *app, f presensiFixture, jam time.Time) *model.PresensiResponse {
	t.Helper()

	testApp.presensi.Now = func() time.Time { return jam }

	response, err := testApp.presensi.Pulang(ctx(), &model.PulangRequest{IDUser: f.karyawan})
	if err != nil {
		t.Fatalf("pulang %s: %v", jam.Format(time.RFC3339), err)
	}

	return response
}

// The button is idempotent in the only way that matters: tapped twice in the
// same shift it creates one row, and the second answer says so out loud
// rather than pretending something just happened.
func TestPresensiMasukKeduaShiftSamaDitolak(t *testing.T) {
	testApp := newApp(t)
	f := newPresensiFixture(t, testApp)

	jam := jamUji(2026, time.March, 17, 7, 5)

	pertama := masukPada(t, testApp, f, jam, &f.unitKerja)
	if pertama.SumberMasuk != entity.SumberPresensiTombol {
		t.Errorf("sumber_masuk = %s, want %s", pertama.SumberMasuk, entity.SumberPresensiTombol)
	}
	if pertama.Status != entity.StatusPresensiBuka {
		t.Errorf("status = %s, want %s", pertama.Status, entity.StatusPresensiBuka)
	}

	testApp.presensi.Now = func() time.Time { return jam.Add(2 * time.Minute) }
	_, err := testApp.presensi.Masuk(ctx(), &model.HadirRequest{
		IDUser: f.karyawan, AktifIDUnitKerja: &f.unitKerja,
	})
	assertKind(t, err, model.KindConflict)

	// One row, not two: the point of the 409 is that nothing was written.
	testApp.presensi.Now = func() time.Time { return jam }
	hariIni, err := testApp.presensi.HariIni(ctx(), &model.GetPresensiHariIniRequest{IDUser: f.karyawan})
	if err != nil {
		t.Fatalf("hari ini: %v", err)
	}

	if hariIni.Pagi.Status != entity.StatusPresensiBuka {
		t.Errorf("pagi.status = %s, want %s", hariIni.Pagi.Status, entity.StatusPresensiBuka)
	}
	if hariIni.Malam.Status != "BELUM_MASUK" {
		t.Errorf("malam.status = %s, want BELUM_MASUK", hariIni.Malam.Status)
	}
}

// Clocking out with nothing open answers 409 naming the fact plainly, not a
// 404 and not an invented row.
func TestPresensiPulangTanpaMasukDitolak(t *testing.T) {
	testApp := newApp(t)
	f := newPresensiFixture(t, testApp)

	testApp.presensi.Now = func() time.Time { return jamUji(2026, time.March, 17, 8, 0) }
	_, err := testApp.presensi.Pulang(ctx(), &model.PulangRequest{IDUser: f.karyawan})
	assertKind(t, err, model.KindConflict)
}

// Clocking in the next day closes yesterday's still-open shift as LUPA_PULANG
// with jam_pulang left NULL — never a guessed hour — and still succeeds in
// opening today's row.
func TestPresensiMasukHariBerikutnyaMenandaiLupaPulang(t *testing.T) {
	testApp := newApp(t)
	f := newPresensiFixture(t, testApp)

	kemarin := masukPada(t, testApp, f, jamUji(2026, time.March, 17, 7, 5), &f.unitKerja)
	hariIni := masukPada(t, testApp, f, jamUji(2026, time.March, 18, 7, 10), &f.unitKerja)

	if kemarin.ID == hariIni.ID {
		t.Fatalf("baris kemarin dan hari ini adalah baris yang sama")
	}

	dari, sampai := "2026-03-17", "2026-03-17"
	list, _, err := testApp.presensi.Search(ctx(), &model.ListPresensiRequest{
		PageRequest: model.PageRequest{Page: 1, Size: 20},
		TanggalDari: &dari, TanggalSampai: &sampai, AktifIDUnitKerja: &f.unitKerja,
	})
	if err != nil {
		t.Fatalf("list kemarin: %v", err)
	}

	if len(list) != 1 {
		t.Fatalf("baris kemarin = %d, want 1", len(list))
	}
	if list[0].Status != entity.StatusPresensiLupaPulang {
		t.Errorf("status kemarin = %s, want %s", list[0].Status, entity.StatusPresensiLupaPulang)
	}
	if list[0].JamPulang != nil {
		t.Errorf("jam_pulang kemarin = %v, want nil — LUPA_PULANG tidak boleh mengarang jam", list[0].JamPulang)
	}

	if hariIni.Status != entity.StatusPresensiBuka {
		t.Errorf("status hari ini = %s, want %s", hariIni.Status, entity.StatusPresensiBuka)
	}
}

// Forgetting to tap Pulang for the morning shift and going straight into the
// night shift is the other event the same LUPA_PULANG rule has to cover: one
// row closes without an hour, the other opens, and the day does NOT count as
// a two-shift day because the morning was never actually completed.
func TestPresensiLupaPulangPagiLaluMasukMalam(t *testing.T) {
	testApp := newApp(t)
	f := newPresensiFixture(t, testApp)

	masukPada(t, testApp, f, jamUji(2026, time.March, 17, 7, 5), &f.unitKerja)
	masukPada(t, testApp, f, jamUji(2026, time.March, 17, 18, 2), &f.unitKerja)

	testApp.presensi.Now = func() time.Time { return jamUji(2026, time.March, 17, 19, 0) }
	hariIni, err := testApp.presensi.HariIni(ctx(), &model.GetPresensiHariIniRequest{IDUser: f.karyawan})
	if err != nil {
		t.Fatalf("hari ini: %v", err)
	}

	if hariIni.Pagi.Status != entity.StatusPresensiLupaPulang {
		t.Errorf("pagi.status = %s, want %s", hariIni.Pagi.Status, entity.StatusPresensiLupaPulang)
	}
	if hariIni.Pagi.JamPulang != nil {
		t.Errorf("pagi.jam_pulang = %v, want nil", hariIni.Pagi.JamPulang)
	}
	if hariIni.Malam.Status != entity.StatusPresensiBuka {
		t.Errorf("malam.status = %s, want %s", hariIni.Malam.Status, entity.StatusPresensiBuka)
	}

	rekap := rekapSatuOrang(t, testApp, 2026, 3, &f.unitKerja)
	if rekap.HariDuaShift != 0 {
		t.Errorf("hari_dua_shift = %d, want 0 — pagi tidak pernah SELESAI", rekap.HariDuaShift)
	}
	if rekap.HariLupaPulangPagi != 1 {
		t.Errorf("hari_lupa_pulang_pagi = %d, want 1", rekap.HariLupaPulangPagi)
	}
}

// A clock-out past midnight closes YESTERDAY's still-open night shift, not
// "today, nothing recorded". No shift crosses midnight with the real hours
// (PAGI ends 17:00, MALAM ends 21:00), but Pulang must not be written as
// "find today's row" regardless — the day a third shift is added or someone
// taps out very late, that shortcut fails silently.
func TestPresensiPulangLewatTengahMalamMenutupShiftMalamKemarin(t *testing.T) {
	testApp := newApp(t)
	f := newPresensiFixture(t, testApp)

	masuk := masukPada(t, testApp, f, jamUji(2026, time.March, 17, 18, 5), &f.unitKerja)

	pulang := pulangPada(t, testApp, f, jamUji(2026, time.March, 18, 0, 30))

	if pulang.ID != masuk.ID {
		t.Fatalf("pulang menutup baris %d, want baris masuk %d", pulang.ID, masuk.ID)
	}
	if pulang.Status != entity.StatusPresensiSelesai {
		t.Errorf("status = %s, want %s", pulang.Status, entity.StatusPresensiSelesai)
	}
	if !pulang.Tanggal.Equal(masuk.Tanggal) {
		t.Errorf("tanggal berubah dari %v ke %v — pulang lewat tengah malam bukan hari baru",
			masuk.Tanggal, pulang.Tanggal)
	}
	if pulang.JamPulang == nil {
		t.Fatal("jam_pulang kosong setelah pulang")
	}
}

// The two-shift day: the whole reason presensi_user_tanggal_shift_uidx
// carries shift in its key, and presensi_terbuka_uidx deliberately does not.
// Keyed on (id_user, tanggal) alone this is refused outright, and the day
// worth the most to payroll becomes the one day that cannot be recorded.
func TestPresensiHariDuaShiftMasukPulangKeduanya(t *testing.T) {
	testApp := newApp(t)
	f := newPresensiFixture(t, testApp)

	pagiMasuk := masukPada(t, testApp, f, jamUji(2026, time.March, 17, 7, 5), &f.unitKerja)
	pagiPulang := pulangPada(t, testApp, f, jamUji(2026, time.March, 17, 17, 1))

	malamMasuk := masukPada(t, testApp, f, jamUji(2026, time.March, 17, 18, 2), &f.unitKerja)
	malamPulang := pulangPada(t, testApp, f, jamUji(2026, time.March, 17, 21, 3))

	if pagiMasuk.ID == malamMasuk.ID {
		t.Fatalf("dua shift menghasilkan satu baris yang sama")
	}
	if pagiPulang.Status != entity.StatusPresensiSelesai || malamPulang.Status != entity.StatusPresensiSelesai {
		t.Fatalf("status pagi/malam = %s/%s, want SELESAI/SELESAI", pagiPulang.Status, malamPulang.Status)
	}

	rekap := rekapSatuOrang(t, testApp, 2026, 3, &f.unitKerja)

	if rekap.HariPagiSelesai != 1 || rekap.HariMalamSelesai != 1 {
		t.Errorf("hari_pagi_selesai/hari_malam_selesai = %d/%d, want 1/1",
			rekap.HariPagiSelesai, rekap.HariMalamSelesai)
	}

	// The figure payroll is waiting for, and the one a client cannot derive
	// from the other two: hari_pagi_selesai + hari_malam_selesai counts this
	// day twice and cannot say the two fell on the same date.
	if rekap.HariDuaShift != 1 {
		t.Errorf("hari_dua_shift = %d, want 1", rekap.HariDuaShift)
	}

	if rekap.TotalMenitKerjaPagi <= 0 || rekap.TotalMenitKerjaMalam <= 0 {
		t.Errorf("total_menit_kerja pagi/malam = %d/%d, want > 0",
			rekap.TotalMenitKerjaPagi, rekap.TotalMenitKerjaMalam)
	}
}

// rekapSatuOrang fetches one employee's month from the recap list — the recap
// endpoint pages over everyone, so tests pull their own employee's row out of
// it.
func rekapSatuOrang(t *testing.T, testApp *app, tahun, bulan int, aktifUnit *int64) model.RekapPresensiResponse {
	t.Helper()

	list, _, err := testApp.presensi.Rekap(ctx(), &model.ListRekapPresensiRequest{
		PageRequest: model.PageRequest{Page: 1, Size: 20},
		Tahun:       tahun, Bulan: bulan, AktifIDUnitKerja: aktifUnit,
	})
	if err != nil {
		t.Fatalf("rekap: %v", err)
	}

	if len(list) != 1 {
		t.Fatalf("baris rekap = %d, want 1", len(list))
	}

	return list[0]
}

// Days are counted, not rows: three shifts across two days are two days
// present.
func TestPresensiRekapMemisahkanShiftDanHari(t *testing.T) {
	testApp := newApp(t)
	f := newPresensiFixture(t, testApp)

	masukPada(t, testApp, f, jamUji(2026, time.March, 17, 7, 5), &f.unitKerja)
	pulangPada(t, testApp, f, jamUji(2026, time.March, 17, 17, 1))

	masukPada(t, testApp, f, jamUji(2026, time.March, 17, 18, 2), &f.unitKerja)
	pulangPada(t, testApp, f, jamUji(2026, time.March, 17, 21, 3))

	masukPada(t, testApp, f, jamUji(2026, time.March, 18, 7, 11), &f.unitKerja)
	pulangPada(t, testApp, f, jamUji(2026, time.March, 18, 17, 2))

	rekap := rekapSatuOrang(t, testApp, 2026, 3, &f.unitKerja)
	if rekap.HariPagiSelesai != 2 || rekap.HariMalamSelesai != 1 || rekap.HariDuaShift != 1 {
		t.Errorf("rekap = pagi %d, malam %d, dua shift %d; want 2/1/1",
			rekap.HariPagiSelesai, rekap.HariMalamSelesai, rekap.HariDuaShift)
	}

	// Another month is another answer, not the same rows filtered
	// client-side.
	kosong, _, err := testApp.presensi.Rekap(ctx(), &model.ListRekapPresensiRequest{
		PageRequest: model.PageRequest{Page: 1, Size: 20},
		Tahun:       2026, Bulan: 4, AktifIDUnitKerja: &f.unitKerja,
	})
	if err != nil {
		t.Fatalf("rekap bulan lain: %v", err)
	}

	if len(kosong) != 0 {
		t.Errorf("rekap bulan lain = %d baris, want 0", len(kosong))
	}
}

// A corrected row must stay readable as corrected forever. Without this the
// module can still be trusted about who was present, but never about who said
// so.
func TestPresensiKoreksiMeninggalkanJejak(t *testing.T) {
	testApp := newApp(t)
	f := newPresensiFixture(t, testApp)

	// A tapped row, so the trail being written over is a real one rather
	// than one that already said KOREKSI.
	awal := masukPada(t, testApp, f, jamUji(2026, time.March, 17, 7, 5), &f.unitKerja)

	dikoreksi, err := testApp.presensi.Update(ctx(), &model.UpdatePresensiRequest{
		ID: awal.ID, ActorID: f.actor,
		JamMasuk:      model.Optional[string]{Present: true, Value: ptr("06:45")},
		AlasanKoreksi: "mesin absen mati, jam diambil dari buku satpam",
	})
	if err != nil {
		t.Fatalf("koreksi: %v", err)
	}

	if dikoreksi.SumberMasuk != entity.SumberPresensiKoreksi {
		t.Errorf("sumber_masuk = %s, want %s", dikoreksi.SumberMasuk, entity.SumberPresensiKoreksi)
	}

	if dikoreksi.DikoreksiOleh == nil || *dikoreksi.DikoreksiOleh != f.actor {
		t.Errorf("dikoreksi_oleh = %v, want %d", dikoreksi.DikoreksiOleh, f.actor)
	}

	if dikoreksi.TsKoreksi == nil {
		t.Errorf("ts_koreksi kosong setelah koreksi")
	}

	if dikoreksi.AlasanKoreksi == nil || *dikoreksi.AlasanKoreksi == "" {
		t.Errorf("alasan_koreksi kosong setelah koreksi")
	}

	// The hour moved to what was asked for, in WIB — the calendar the office
	// keeps.
	if jam := dikoreksi.JamMasuk.In(zonaUjiWIB).Format("15:04"); jam != "06:45" {
		t.Errorf("jam_masuk = %s, want 06:45", jam)
	}

	// And it stayed on its own day. Moving an attendance to another date is
	// not a correction, so a corrected hour is applied to the row's stored
	// tanggal.
	if !dikoreksi.Tanggal.Equal(awal.Tanggal) {
		t.Errorf("tanggal berubah dari %v ke %v", awal.Tanggal, dikoreksi.Tanggal)
	}
}

// Filling jam_pulang on a LUPA_PULANG row through a correction closes it as
// SELESAI on its own, with the touched column's own sumber turning KOREKSI —
// status is recomputed, never accepted from the form.
func TestPresensiKoreksiJamPulangMengubahLupaPulangJadiSelesai(t *testing.T) {
	testApp := newApp(t)
	f := newPresensiFixture(t, testApp)

	kemarin := masukPada(t, testApp, f, jamUji(2026, time.March, 17, 7, 5), &f.unitKerja)
	masukPada(t, testApp, f, jamUji(2026, time.March, 18, 7, 10), &f.unitKerja)

	dikoreksi, err := testApp.presensi.Update(ctx(), &model.UpdatePresensiRequest{
		ID: kemarin.ID, ActorID: f.actor,
		JamPulang:     model.Optional[string]{Present: true, Value: ptr("17:00")},
		AlasanKoreksi: "lupa tap pulang, jam diambil dari CCTV",
	})
	if err != nil {
		t.Fatalf("koreksi jam pulang: %v", err)
	}

	if dikoreksi.Status != entity.StatusPresensiSelesai {
		t.Errorf("status = %s, want %s", dikoreksi.Status, entity.StatusPresensiSelesai)
	}
	if dikoreksi.SumberPulang == nil || *dikoreksi.SumberPulang != entity.SumberPresensiKoreksi {
		t.Errorf("sumber_pulang = %v, want %s", dikoreksi.SumberPulang, entity.SumberPresensiKoreksi)
	}
	if dikoreksi.DurasiKerjaMenit == nil {
		t.Errorf("durasi_kerja_menit kosong walau jam_pulang sudah terisi")
	}
}

// jam_pulang may never be nulled back out through a correction: reopening a
// closed shift would reactivate presensi_terbuka_uidx for a day already gone.
func TestPresensiKoreksiJamPulangTidakBolehDikosongkan(t *testing.T) {
	testApp := newApp(t)
	f := newPresensiFixture(t, testApp)

	masuk := masukPada(t, testApp, f, jamUji(2026, time.March, 17, 7, 5), &f.unitKerja)
	pulangPada(t, testApp, f, jamUji(2026, time.March, 17, 17, 1))

	_, err := testApp.presensi.Update(ctx(), &model.UpdatePresensiRequest{
		ID: masuk.ID, ActorID: f.actor,
		JamPulang:     model.Optional[string]{Present: true, Value: nil},
		AlasanKoreksi: "mencoba membuka kembali",
	})
	assertKind(t, err, model.KindInvalid)
}

// A correction with no reason is refused: alasan_koreksi is the only record
// of why somebody's hour changed. Policy, not constraint — the
// keterangan_selisih precedent.
func TestPresensiKoreksiTanpaAlasanDitolak(t *testing.T) {
	testApp := newApp(t)
	f := newPresensiFixture(t, testApp)

	awal := masukPada(t, testApp, f, jamUji(2026, time.March, 17, 7, 5), &f.unitKerja)

	_, err := testApp.presensi.Update(ctx(), &model.UpdatePresensiRequest{
		ID: awal.ID, ActorID: f.actor,
		JamMasuk: model.Optional[string]{Present: true, Value: ptr("06:45")},
	})
	if err == nil {
		t.Fatalf("koreksi tanpa alasan seharusnya ditolak")
	}
}

// Correcting the hour never re-infers the shift: they are two decisions, and
// tying them together means a one-minute typo silently moves somebody into
// the other shift.
func TestPresensiKoreksiJamTidakMenyimpulkanUlangShift(t *testing.T) {
	testApp := newApp(t)
	f := newPresensiFixture(t, testApp)

	// A row that started life as MALAM (tapped at 18:00, past the 17:30
	// boundary), corrected to an hour that would infer PAGI if anything ever
	// re-inferred it from the new value.
	awal := masukPada(t, testApp, f, jamUji(2026, time.March, 17, 18, 0), &f.unitKerja)

	dikoreksi, err := testApp.presensi.Update(ctx(), &model.UpdatePresensiRequest{
		ID: awal.ID, ActorID: f.actor,
		JamMasuk:      model.Optional[string]{Present: true, Value: ptr("15:30")},
		AlasanKoreksi: "datang lebih awal untuk shift malam",
	})
	if err != nil {
		t.Fatalf("koreksi: %v", err)
	}

	if dikoreksi.Shift != entity.ShiftMalam {
		t.Errorf("shift = %s, want %s — koreksi jam tidak boleh memindahkan shift",
			dikoreksi.Shift, entity.ShiftMalam)
	}
}

// Correcting a shift into one the person already has that day collides with
// presensi_user_tanggal_shift_uidx, and that surfaces as a 409 rather than a
// 500.
func TestPresensiKoreksiShiftBentrokDitolak(t *testing.T) {
	testApp := newApp(t)
	f := newPresensiFixture(t, testApp)

	masukPada(t, testApp, f, jamUji(2026, time.March, 17, 7, 5), &f.unitKerja)
	malam := masukPada(t, testApp, f, jamUji(2026, time.March, 17, 18, 2), &f.unitKerja)

	_, err := testApp.presensi.Update(ctx(), &model.UpdatePresensiRequest{
		ID: malam.ID, ActorID: f.actor,
		Shift:         model.Optional[string]{Present: true, Value: ptr(entity.ShiftPagi)},
		AlasanKoreksi: "salah shift",
	})
	assertKind(t, err, model.KindConflict)
}

// A shift with no row answers status = BELUM_MASUK, not 404 — the synthetic
// answer periode already gives a month nobody has closed. And the server
// names the shift it is now, so no client has to infer that from a phone's
// own clock.
func TestPresensiHariIniMenjawabKeduaShift(t *testing.T) {
	testApp := newApp(t)
	f := newPresensiFixture(t, testApp)

	jam := jamUji(2026, time.March, 17, 7, 0)
	testApp.presensi.Now = func() time.Time { return jam }

	hariIni, err := testApp.presensi.HariIni(ctx(), &model.GetPresensiHariIniRequest{IDUser: f.karyawan})
	if err != nil {
		t.Fatalf("hari ini: %v", err)
	}

	if hariIni.Pagi.Status != "BELUM_MASUK" || hariIni.Malam.Status != "BELUM_MASUK" {
		t.Errorf("belum menekan tombol, tapi status = %s/%s", hariIni.Pagi.Status, hariIni.Malam.Status)
	}
	if hariIni.ShiftSekarang != entity.ShiftPagi {
		t.Errorf("shift_sekarang = %s, want %s", hariIni.ShiftSekarang, entity.ShiftPagi)
	}

	masukPada(t, testApp, f, jam, &f.unitKerja)

	testApp.presensi.Now = func() time.Time { return jam }
	sesudah, err := testApp.presensi.HariIni(ctx(), &model.GetPresensiHariIniRequest{IDUser: f.karyawan})
	if err != nil {
		t.Fatalf("hari ini setelah tap: %v", err)
	}

	if sesudah.Pagi.Status != entity.StatusPresensiBuka || sesudah.Pagi.JamMasuk == nil {
		t.Errorf("shift yang baru ditekan tidak terlihat di hari-ini: %+v", sesudah.Pagi)
	}
}

// The list pages stably and filters by shift without the COUNT disagreeing
// with the rows it claims to be counting.
func TestPresensiListFilterShift(t *testing.T) {
	testApp := newApp(t)
	f := newPresensiFixture(t, testApp)

	for hari := 1; hari <= 3; hari++ {
		masukPada(t, testApp, f, jamUji(2026, time.March, hari, 7, 5), &f.unitKerja)
		masukPada(t, testApp, f, jamUji(2026, time.March, hari, 18, 2), &f.unitKerja)
	}

	malam := entity.ShiftMalam

	list, paging, err := testApp.presensi.Search(ctx(), &model.ListPresensiRequest{
		PageRequest: model.PageRequest{Page: 1, Size: 20},
		Shift:       &malam, AktifIDUnitKerja: &f.unitKerja,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if paging.TotalItem != 3 || len(list) != 3 {
		t.Fatalf("total_item %d / baris %d, want 3/3", paging.TotalItem, len(list))
	}

	for _, baris := range list {
		if baris.Shift != entity.ShiftMalam {
			t.Errorf("filter shift bocor: %s", baris.Shift)
		}
	}

	// A date range narrows it further, and the count follows the rows
	// rather than being computed over a different FROM.
	dari, sampai := "2026-03-02", "2026-03-03"

	terbatas, paging, err := testApp.presensi.Search(ctx(), &model.ListPresensiRequest{
		PageRequest: model.PageRequest{Page: 1, Size: 20},
		TanggalDari: &dari, TanggalSampai: &sampai, AktifIDUnitKerja: &f.unitKerja,
	})
	if err != nil {
		t.Fatalf("list rentang: %v", err)
	}

	if paging.TotalItem != 4 || len(terbatas) != 4 {
		t.Fatalf("rentang dua hari: total_item %d / baris %d, want 4/4", paging.TotalItem, len(terbatas))
	}
}

// The daily sweep (fase 5) marks a row LUPA_PULANG only once its own date has
// passed — a shift still BUKA the SAME day is left alone, because the person
// tapping Masuk again is what already handles it correctly.
func TestPresensiSapuanLupaPulangHanyaTanggalYangSudahLewat(t *testing.T) {
	testApp := newApp(t)
	f := newPresensiFixture(t, testApp)

	masukPada(t, testApp, f, jamUji(2026, time.March, 17, 7, 5), &f.unitKerja)
	masukPada(t, testApp, f, jamUji(2026, time.March, 18, 7, 5), nil)

	testApp.presensi.Now = func() time.Time { return jamUji(2026, time.March, 18, 20, 0) }

	jumlah, err := testApp.presensi.SapuanLupaPulang(ctx())
	if err != nil {
		t.Fatalf("sapuan: %v", err)
	}

	// kemarin was already closed to LUPA_PULANG the moment "hariIniBuka" was
	// tapped (Masuk's own same-transaction step), so the sweep itself finds
	// nothing new to touch on this run: proof the sweep does not re-mark an
	// already-LUPA_PULANG row and does not touch today's still-open one.
	if jumlah != 0 {
		t.Errorf("sapuan = %d, want 0 (kemarin sudah LUPA_PULANG lewat tap, hari ini masih BUKA)", jumlah)
	}

	dari := "2026-03-17"
	list, _, err := testApp.presensi.Search(ctx(), &model.ListPresensiRequest{
		PageRequest: model.PageRequest{Page: 1, Size: 20},
		TanggalDari: &dari, TanggalSampai: &dari, IDUser: &f.karyawan,
	})
	if err != nil {
		t.Fatalf("list kemarin: %v", err)
	}
	if len(list) != 1 || list[0].Status != entity.StatusPresensiLupaPulang {
		t.Fatalf("baris kemarin tidak LUPA_PULANG: %+v", list)
	}

	sampaiHariIni := "2026-03-18"
	hariIniList, _, err := testApp.presensi.Search(ctx(), &model.ListPresensiRequest{
		PageRequest: model.PageRequest{Page: 1, Size: 20},
		TanggalDari: &sampaiHariIni, TanggalSampai: &sampaiHariIni, IDUser: &f.karyawan,
	})
	if err != nil {
		t.Fatalf("list hari ini: %v", err)
	}
	if len(hariIniList) != 1 || hariIniList[0].Status != entity.StatusPresensiBuka {
		t.Fatalf("baris hari ini seharusnya masih BUKA, dapat: %+v", hariIniList)
	}
}
