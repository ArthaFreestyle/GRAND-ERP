package usecase

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/model/converter"
	"Arthafreestyle/ERP/internal/repository"

	"github.com/go-playground/validator/v10"
	"github.com/sirupsen/logrus"
)

// jamOnly parses jam_masuk/jam_pulang on the write paths that accept one: a
// wall clock in WIB, no date and no zone, because the date is never a caller's
// to choose.
const jamOnly = "15:04"

// PresensiUseCase holds the rules for attendance, and it is the first module in
// this project that touches neither kartu_stok nor money.
//
// What that absence removes: no document number, no periode check, no room
// freeze, no big.Rat, no posting engine. What is deliberately kept from the
// project's habits is only what still applies — identity comes from the
// session and never from a body, the vocabularies are guarded by CHECKs rather
// than by Go, a status column is always recomputed and never accepted from a
// form, and a row someone typed or moved on another person's behalf stays
// readable as such forever.
//
// Now is a seam over time.Now, never a client input — the server's clock is
// still the sole source of "when", exactly as the issue requires. What it buys
// is a usecase whose tests can stand in for "the next day" or "just past
// midnight" without a real clock actually crossing either, which a module
// whose entire shape hinges on wall-clock boundaries has no honest way to test
// otherwise.
type PresensiUseCase struct {
	DB                 *sql.DB
	Log                *logrus.Logger
	Validate           *validator.Validate
	PresensiRepository *repository.PresensiRepository
	Now                func() time.Time
}

func NewPresensiUseCase(
	db *sql.DB,
	log *logrus.Logger,
	validate *validator.Validate,
	presensiRepository *repository.PresensiRepository,
) *PresensiUseCase {
	return &PresensiUseCase{
		DB:                 db,
		Log:                log,
		Validate:           validate,
		PresensiRepository: presensiRepository,
		Now:                time.Now,
	}
}

// Masuk records one clock-in tap.
//
// Both the date and the shift come from the server's own clock, never from the
// request: one button stays one button, and nobody can claim a night shift
// while tapping at eight in the morning. The caller supplies nothing at all.
//
// A second tap for the same (tanggal, shift) answers 409 rather than a silent
// 200 or a second row — a phone on a bad connection will send the same tap
// twice, and the honest answer is "this is already recorded" — enough for the
// client to draw the right state without pretending something just happened. A
// tap for the OTHER shift on the same day is not a duplicate at all; it is the
// two-shift day this whole module exists to record. And if this user still has
// a DIFFERENT shift open (forgot to tap Pulang, or tapped Pulang for yesterday
// and is now starting today), that old row is closed as LUPA_PULANG in the same
// transaction — never given a guessed jam_pulang.
func (c *PresensiUseCase) Masuk(ctx context.Context, request *model.HadirRequest) (*model.PresensiResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	jam := c.Now()
	tanggal := tanggalPresensi(jam)
	shift := simpulkanShift(jam)

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback() // no-op once the transaction is committed
	}()

	if err := c.tolakJikaSlotTerisi(ctx, tx, request.IDUser, tanggal, shift); err != nil {
		return nil, err
	}

	// Any shift this user still has open belongs, by the check above, to a
	// DIFFERENT (tanggal, shift) — otherwise it would already have been
	// caught as the same-slot duplicate. It is closed here, in the same
	// transaction as the new row, with jam_pulang left NULL: the one fact
	// known for certain is that nobody tapped Pulang for it, and inventing an
	// hour would be the one permanent lie this module refuses to tell.
	terbuka, err := c.PresensiRepository.FindTerbuka(ctx, tx, request.IDUser)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err == nil {
		if err := c.PresensiRepository.TandaiLupaPulang(ctx, tx, terbuka.ID); err != nil {
			return nil, err
		}
	}

	presensi := &entity.Presensi{
		IDUser:      request.IDUser,
		Tanggal:     tanggal,
		Shift:       shift,
		JamMasuk:    jam,
		Status:      entity.StatusPresensiBuka,
		IDUnitKerja: request.AktifIDUnitKerja,
		SumberMasuk: entity.SumberPresensiTombol,
	}

	if request.IPMasuk != "" {
		presensi.IPMasuk = &request.IPMasuk
	}

	// The check above is only for the friendlier message. Two taps arriving
	// together both pass it and one then loses at the database, which is
	// what actually guarantees the result — the check-then-insert rule this
	// codebase states everywhere else. Two different unique indexes can
	// reject this insert, and the message has to say which:
	// presensi_user_tanggal_shift_uidx means this slot was already taken by a
	// tap that landed between the check and this insert;
	// presensi_terbuka_uidx means a concurrent tap already opened (or is
	// opening) a different shift for this same user, since only one open row
	// per user is ever allowed to exist.
	if err := c.PresensiRepository.Create(ctx, tx, presensi); err != nil {
		if constraint, ok := repository.UniqueViolation(err); ok {
			switch constraint {
			case "presensi_terbuka_uidx":
				return nil, model.Conflict("masih ada shift lain yang terbuka; coba lagi")
			default:
				return nil, model.Conflict(fmt.Sprintf("sudah presensi shift %s hari ini", shift))
			}
		}

		return nil, invalidOnForeignKey(err, "id_unit_kerja menunjuk unit kerja yang tidak ada")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	// Re-read so the response carries the names the write itself could not
	// join for.
	return c.get(ctx, presensi.ID)
}

// tolakJikaSlotTerisi refuses a tap when a row already exists for exactly this
// (tanggal, shift) — whatever its status. A BUKA row there means the button
// itself was pressed twice; a SELESAI row means the person already clocked out
// of that shift and coming back is a correction, not a second attendance,
// neither of which this endpoint may create a second row for.
func (c *PresensiUseCase) tolakJikaSlotTerisi(ctx context.Context, tx *sql.Tx, idUser int64, tanggal time.Time, shift string) error {
	sama, err := c.PresensiRepository.FindByUserTanggalShift(ctx, tx, idUser, tanggal, shift)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}

		return err
	}

	switch sama.Status {
	case entity.StatusPresensiBuka:
		return model.Conflict(fmt.Sprintf(
			"sudah presensi masuk shift %s hari ini pukul %s",
			shift, sama.JamMasuk.In(zonaWIB).Format(jamOnly),
		))
	default:
		return model.Conflict(fmt.Sprintf("sudah presensi shift %s hari ini dan sudah selesai", shift))
	}
}

// Pulang closes the one shift this user currently has open, whatever date or
// shift it belongs to — presensi_terbuka_uidx is what guarantees there is at
// most one, so Pulang never has to guess which row it means.
//
// It never re-infers the shift and never compares the tap against the shift's
// nominal end time: the shift was decided once, at Masuk, and lembur or a late
// clock-out is not this module's to judge.
func (c *PresensiUseCase) Pulang(ctx context.Context, request *model.PulangRequest) (*model.PresensiResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	var ip *string
	if request.IPPulang != "" {
		ip = &request.IPPulang
	}

	presensi, err := c.PresensiRepository.Pulang(ctx, c.DB, request.IDUser, c.Now(), ip)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, model.Conflict("belum presensi masuk")
		}

		// jam_pulang > jam_masuk is a CHECK (presensi_jam_check); the trigger
		// clock cannot go backwards, so this can only fire from a clock skew
		// between two requests on the same connection pool — invalidOnCheck
		// surfaces it rather than re-validating it in Go.
		return nil, invalidOnCheck(err, "jam_pulang harus setelah jam_masuk")
	}

	return c.get(ctx, presensi.ID)
}

// HariIni answers what the button should look like right now, for BOTH shifts
// at once.
//
// A shift with no row answers status = BELUM_MASUK rather than 404 — a
// synthetic answer for something that has not happened yet, the same shape
// periode gives a month nobody has closed. The shift the server thinks it is
// right now rides along, because a client inferring that from its own clock
// would put someone in the wrong shift every time the phone drifts a few
// minutes past 17:30.
func (c *PresensiUseCase) HariIni(ctx context.Context, request *model.GetPresensiHariIniRequest) (*model.PresensiHariIniResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	jam := c.Now()
	tanggal := tanggalPresensi(jam)

	harian, err := c.PresensiRepository.FindByUserTanggal(ctx, c.DB, request.IDUser, tanggal)
	if err != nil {
		return nil, err
	}

	response := &model.PresensiHariIniResponse{
		Tanggal:       tanggal,
		ShiftSekarang: simpulkanShift(jam),
		Pagi:          model.PresensiShiftHariIniResponse{Shift: entity.ShiftPagi, Status: "BELUM_MASUK"},
		Malam:         model.PresensiShiftHariIniResponse{Shift: entity.ShiftMalam, Status: "BELUM_MASUK"},
	}

	for i := range harian {
		baris := &harian[i]

		terisi := model.PresensiShiftHariIniResponse{
			Shift:            baris.Shift,
			Status:           baris.Status,
			ID:               &baris.ID,
			JamMasuk:         &baris.JamMasuk,
			JamPulang:        baris.JamPulang,
			DurasiKerjaMenit: durasiKerjaMenitBaris(baris),
		}

		if baris.Shift == entity.ShiftPagi {
			response.Pagi = terisi
		} else {
			response.Malam = terisi
		}
	}

	return response, nil
}

// durasiKerjaMenitBaris is HariIni's own computation, since it works from
// *entity.Presensi rather than the converter's package-level helper.
func durasiKerjaMenitBaris(presensi *entity.Presensi) *int {
	if presensi.JamPulang == nil {
		return nil
	}

	menit := int(presensi.JamPulang.Sub(presensi.JamMasuk).Minutes())

	return &menit
}

// Search pages over attendance rows — both GET /presensi (SUPERADMIN, scoped
// to the active unit) and GET /presensi/saya (any caller, forced to their own
// id_user and never scoped by unit) share this, differing only in what the
// controller set on the request before calling it.
//
// Rows outside the caller's active unit are omitted silently rather than
// answering 403 or 404: a list has no single resource identity to 404 against,
// and a page with fewer rows is what every other scoped list in this project
// already looks like.
func (c *PresensiUseCase) Search(ctx context.Context, request *model.ListPresensiRequest) ([]model.PresensiResponse, *model.PageMetadata, error) {
	request.Normalize()

	if err := c.Validate.Struct(request); err != nil {
		return nil, nil, err
	}

	filter := repository.PresensiFilter{
		IDUser:           request.IDUser,
		Shift:            request.Shift,
		Status:           request.Status,
		AktifIDUnitKerja: request.AktifIDUnitKerja,
	}

	if request.TanggalDari != nil {
		dari, err := time.Parse(dateOnly, *request.TanggalDari)
		if err != nil {
			return nil, nil, model.Invalid("tanggal_dari tidak valid")
		}

		filter.TanggalDari = &dari
	}

	if request.TanggalSampai != nil {
		sampai, err := time.Parse(dateOnly, *request.TanggalSampai)
		if err != nil {
			return nil, nil, model.Invalid("tanggal_sampai tidak valid")
		}

		filter.TanggalSampai = &sampai
	}

	list, total, err := c.PresensiRepository.Search(ctx, c.DB, filter, request.Size, request.Offset())
	if err != nil {
		return nil, nil, err
	}

	return converter.PresensiToResponses(list), pageMetadata(&request.PageRequest, total), nil
}

// Rekap pages over the monthly recap, one row per employee — the whole page in
// one query, never one query per employee.
func (c *PresensiUseCase) Rekap(ctx context.Context, request *model.ListRekapPresensiRequest) ([]model.RekapPresensiResponse, *model.PageMetadata, error) {
	request.Normalize()

	if err := c.Validate.Struct(request); err != nil {
		return nil, nil, err
	}

	list, total, err := c.PresensiRepository.RekapBulanan(
		ctx, c.DB, request.Tahun, request.Bulan, nil,
		request.AktifIDUnitKerja, request.Size, request.Offset(),
	)
	if err != nil {
		return nil, nil, err
	}

	return converter.RekapPresensiToResponses(list), pageMetadata(&request.PageRequest, total), nil
}

// Update corrects a row and leaves a trail doing it.
//
// Correcting the hour(s) and correcting the shift are separate decisions, and
// this never derives one from the other: fixing a one-minute typo must not
// quietly move somebody into the other shift, and someone who genuinely worked
// the night shift after tapping in at four in the afternoon needs the shift
// moved without their hour being rewritten to match.
//
// A corrected hour stays on the row's own date by construction — jam_masuk and
// jam_pulang are given as wall clocks and applied to the stored tanggal —
// because moving an attendance to another day is not a correction, it is a
// different day's attendance.
func (c *PresensiUseCase) Update(ctx context.Context, request *model.UpdatePresensiRequest) (*model.PresensiResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	// jam_masuk is NOT NULL and jam_pulang may never be reopened once filled,
	// so an explicit null is refused rather than silently treated as "leave
	// it alone" — the caller asked for something the column may not hold.
	if request.JamMasuk.Clears() {
		return nil, model.Invalid("jam_masuk cannot be null")
	}
	if request.JamPulang.Clears() {
		return nil, model.Invalid("jam_pulang cannot be null; koreksi memindahkan jam, bukan membuka kembali shift")
	}
	if request.Shift.Clears() {
		return nil, model.Invalid("shift cannot be null")
	}

	if !request.JamMasuk.Set() && !request.JamPulang.Set() && !request.Shift.Set() {
		return nil, model.Invalid("no fields to update")
	}

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	// The stored row is read for one reason: a corrected hour is a wall clock
	// and needs the row's own tanggal to become a moment. Nothing else is
	// taken from it, and the UPDATE still decides on its own whether the id
	// exists.
	tersimpan, err := c.PresensiRepository.FindByID(ctx, tx, request.ID)
	if err != nil {
		return nil, notFoundOnNoRows(err, "presensi not found")
	}

	patch := repository.PresensiPatch{
		Shift:         request.Shift.Value,
		DikoreksiOleh: request.ActorID,
		TsKoreksi:     c.Now(),
		AlasanKoreksi: request.AlasanKoreksi,
	}

	if request.JamMasuk.Set() {
		jam, err := jamPadaTanggal(tersimpan.Tanggal, *request.JamMasuk.Value)
		if err != nil {
			return nil, err
		}

		patch.JamMasuk = &jam
	}

	if request.JamPulang.Set() {
		jam, err := jamPadaTanggal(tersimpan.Tanggal, *request.JamPulang.Value)
		if err != nil {
			return nil, err
		}

		patch.JamPulang = &jam
	}

	if _, err := c.PresensiRepository.Update(ctx, tx, request.ID, patch); err != nil {
		return nil, notFoundOnNoRows(
			conflictOnUnique(
				invalidOnCheck(
					invalidOnForeignKey(err, "dikoreksi_oleh menunjuk user yang tidak ada"),
					"jam_pulang harus setelah jam_masuk",
				),
				"orang ini sudah punya presensi di shift itu pada tanggal yang sama",
			),
			"presensi not found",
		)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.get(ctx, request.ID)
}

// get re-reads a row for the response, so it carries the joined names a
// write's own RETURNING cannot produce.
func (c *PresensiUseCase) get(ctx context.Context, id int64) (*model.PresensiResponse, error) {
	presensi, err := c.PresensiRepository.FindByID(ctx, c.DB, id)
	if err != nil {
		return nil, notFoundOnNoRows(err, "presensi not found")
	}

	return converter.PresensiToResponse(presensi), nil
}

// SapuanLupaPulang is the worker's third job (isu #40 fase 5). Rows a tap
// legitimately closes are handled by Masuk's own LUPA_PULANG step; this exists
// for the ones nobody ever tapped again for — resigned, on long leave, or sick
// for a week — which would otherwise hang open forever and read to the recap
// as someone still clocked in since last Tuesday.
//
// Swept per date, never per shift: a PAGI row still BUKA at eight in the
// evening of the SAME day is left alone on purpose, because the person may
// still be there and a MALAM tap already handles that correctly. batas is
// today's date in WIB, so only rows dated strictly before today are touched.
//
// This job repairs, and that does not contradict isu #25's job reporting
// without ever repairing: there a balance mismatch means a bug with no honest
// fix available from outside the trigger. Here the fact is already known with
// certainty — the person never tapped Pulang — and LUPA_PULANG records exactly
// that fact without inventing a single number. What stays forbidden is the
// same as everywhere else in this module: guessing jam_pulang.
func (c *PresensiUseCase) SapuanLupaPulang(ctx context.Context) (int, error) {
	// One connection for the whole job: a session-level advisory lock belongs
	// to the connection that took it, and releasing it from another pooled
	// connection would silently do nothing.
	conn, err := c.DB.Conn(ctx)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = conn.Close()
	}()

	locked, err := c.PresensiRepository.TryLockSapuan(ctx, conn)
	if err != nil {
		return 0, err
	}

	if !locked {
		c.Log.Debug("sapuan presensi: worker lain sedang berjalan")

		return 0, nil
	}
	defer func() {
		if err := c.PresensiRepository.UnlockSapuan(ctx, conn); err != nil {
			c.Log.WithError(err).Error("sapuan presensi: gagal melepas advisory lock")
		}
	}()

	batas := tanggalPresensi(c.Now())

	jumlah, err := c.PresensiRepository.SapuanLupaPulang(ctx, conn, batas)
	if err != nil {
		return 0, err
	}

	return int(jumlah), nil
}

// jamPadaTanggal turns a wall clock in WIB into the moment it names on a given
// calendar date.
//
// The date always comes from the server side of the request — the stored row,
// or a tanggal already parsed and validated — so the hour and the date can
// never end up describing different days. There is no CHECK behind those two
// columns to catch it if they did: timestamptz::date depends on the session's
// TimeZone, which makes it too unstable to appear in one.
func jamPadaTanggal(tanggal time.Time, jam string) (time.Time, error) {
	parsed, err := time.Parse(jamOnly, jam)
	if err != nil {
		return time.Time{}, model.Invalid("jam harus berformat HH:MM")
	}

	return time.Date(
		tanggal.Year(), tanggal.Month(), tanggal.Day(),
		parsed.Hour(), parsed.Minute(), 0, 0, zonaWIB,
	), nil
}
