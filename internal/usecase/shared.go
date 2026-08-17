package usecase

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/repository"
)

// notFoundOnNoRows maps an absent row to a 404. Every FindByID and every
// UPDATE ... RETURNING funnels through here, so no usecase has to remember that
// a missing id surfaces as sql.ErrNoRows.
func notFoundOnNoRows(err error, message string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return model.NotFound(message)
	}

	return err
}

// conflictOnUnique maps a unique-index violation to a 409, leaving anything else
// untouched.
//
// The Exists* pre-check in each usecase is not a guarantee: two concurrent
// requests can both pass it and one then loses at the database. This turns that
// loser into a 409 instead of a 500. The pre-check is kept only because it
// produces a message naming the field that collided.
func conflictOnUnique(err error, message string) error {
	if repository.IsUniqueViolation(err) {
		return model.Conflict(message)
	}

	return err
}

// invalidOnForeignKey maps a missing referenced row to a 400, leaving anything else
// untouched.
//
// Granting a role checks the ids first, but that check is not a guarantee: the role
// can disappear between the check and the INSERT. This turns the loser of that race
// into a 400 naming the problem instead of a 500.
func invalidOnForeignKey(err error, message string) error {
	if repository.IsForeignKeyViolation(err) {
		return model.Invalid(message)
	}

	return err
}

// conflictOnExclusion maps an EXCLUDE constraint violation to a 409.
//
// This is the only guard against two price versions being valid at the same time.
// Checking for an overlap in Go first cannot replace it — the check spans rows, so two
// concurrent requests can both find no overlap and then both insert.
func conflictOnExclusion(err error, message string) error {
	if repository.IsExclusionViolation(err) {
		return model.Conflict(message)
	}

	return err
}

// invalidOnCheck maps a CHECK constraint or trigger rejection to a 400, leaving
// anything else untouched.
//
// The kartu_stok engine refuses two things this way: a movement that would drive
// stock below zero, and one dated inside a periode already closed. Neither is a
// server fault and neither can be pre-checked honestly — the balance is computed
// inside the trigger, under an advisory lock, precisely so no reader can decide it
// first.
//
// The database's own message is not passed through. It names internal ids and this
// codebase does not leak internals to clients, so each call site supplies text that
// makes sense to an operator.
func invalidOnCheck(err error, message string) error {
	if repository.IsCheckViolation(err) {
		return model.Invalid(message)
	}

	return err
}

// periksaPeriode refuses a movement dated inside a closed month, before the
// kartu_stok trigger has to.
//
// This is not the guard and must not be mistaken for one — the trigger is, and it
// decides under an advisory lock that no reader can get in front of. What this buys
// is a message naming one cause. A trigger's RAISE carries no constraint name, so
// invalidOnCheck cannot tell a closed period from insufficient stock, and every call
// site has to say "periode sudah TUTUP atau saldo tidak mencukupi" — an operator
// reading that learns nothing about which of the two to go and fix. Same relationship
// as ExistsByKode has to a unique index.
//
// It answers 400 rather than 409, matching what the trigger's rejection maps to. The
// two paths describe the same refusal and should not arrive as different statuses
// depending on who noticed first.
func periksaPeriode(ctx context.Context, db repository.DBTX, periode *repository.PeriodeRepository, tanggal time.Time) error {
	tutup, err := periode.IsTutup(ctx, db, tanggal)
	if err != nil {
		return err
	}

	if tutup {
		return model.Invalid(fmt.Sprintf(
			"periode %04d-%02d sudah TUTUP", tanggal.Year(), int(tanggal.Month()),
		))
	}

	return nil
}

// periksaRuangUnitAktif refuses a write naming a ruang outside the caller's
// active unit_kerja — isu #12 fase 5. A nil aktifIDUnitKerja means either the
// caller's active grant applies everywhere (the SUPERADMIN shape) or,
// defensively, that there is no active context at all; either way nothing is
// checked, the same reading "id_unit_kerja IS NULL" already carries
// everywhere else in this codebase.
//
// This is validation, not a default: id_ruang is used exactly as the request
// sent it, never silently substituted or corrected. The same rule
// created_by follows via middleware.SessionFrom — a server that fills in a
// default but still accepts whatever the body said makes the scoping
// decorative rather than real.
//
// Unlike periksaPeriode, this is not a friendlier message ahead of a database
// guard — there is no trigger behind it, because ruang.id_unit_kerja cannot
// change after a room is created (ruang has no PATCH), so nothing can
// invalidate this check between reading it and the write that follows.
//
// An unknown id_ruang is deliberately let through here (returns nil): that is
// a different failure — "this room does not exist" — and answering it is the
// foreign key's job, not this one's.
func periksaRuangUnitAktif(ctx context.Context, db repository.DBTX, ruang *repository.RuangRepository, aktifIDUnitKerja *int64, idRuang int64) error {
	if aktifIDUnitKerja == nil {
		return nil
	}

	idUnitKerja, err := ruang.IDUnitKerjaByID(ctx, db, idRuang)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}

		return err
	}

	if idUnitKerja != *aktifIDUnitKerja {
		return model.Forbidden("id_ruang is outside your active unit_kerja")
	}

	return nil
}

// conflictOnRuangBeku maps the kartu_stok trigger's room-freeze rejection (isu
// #15, SQLSTATE 55000 — object_not_in_prerequisite_state) to a 409, leaving
// anything else untouched.
//
// 409 rather than 400: the request itself is not malformed, the room it names is
// simply not ready yet, the same reasoning conflictOnTransisi already applies to a
// document that moved out from under a caller.
func conflictOnRuangBeku(err error, message string) error {
	if repository.IsObjectNotInPrerequisiteState(err) {
		return model.Conflict(message)
	}

	return err
}

// periksaRuangBeku refuses a posting into a ruang currently frozen by an open
// stok_opname, before the kartu_stok trigger has to — isu #15.
//
// This is not the guard and must not be mistaken for one — the trigger is, and it
// decides under the ruang: advisory lock that no reader can get in front of. What
// this buys is a message naming the opname's own nomor and the room, which a
// trigger's RAISE cannot: an operator whose posting was refused has to know who to
// chase, not just that something is wrong.
//
// Called at Posting and Batal in every module that writes kartu_stok —
// stokOpname.FindOpenByRuang answers sql.ErrNoRows for a free room, which this
// reads as "nothing to check", the same shape periksaPeriode and
// periksaRuangUnitAktif already use for their own "nothing to check" case.
func periksaRuangBeku(ctx context.Context, db repository.DBTX, stokOpname *repository.StokOpnameRepository, idRuang int64) error {
	opname, err := stokOpname.FindOpenByRuang(ctx, db, idRuang)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}

		return err
	}

	return model.Conflict(fmt.Sprintf(
		"ruang %s sedang diopname (nomor %s); transaksi tidak dapat diposting",
		opname.NamaRuang, opname.Nomor,
	))
}

// diLuarUnitAktif reports whether a room's unit_kerja falls outside the
// caller's active unit — isu #12 fase 6, the read-side counterpart of
// periksaRuangUnitAktif. A nil aktifIDUnitKerja (global grant, or
// defensively no active context) excludes nothing, the same reading nil
// carries everywhere else in this codebase.
//
// Every caller of this must answer with model.NotFound, never
// model.Forbidden: a scoped read makes a document outside the active unit
// look like it does not exist, the same as a genuinely unknown id. Answering
// 403 here would confirm the document exists, which is exactly the
// information scoping a read is supposed to withhold.
func diLuarUnitAktif(idUnitKerjaRuang int64, aktifIDUnitKerja *int64) bool {
	return aktifIDUnitKerja != nil && idUnitKerjaRuang != *aktifIDUnitKerja
}

// conflictOnTransisi maps a guarded status change that matched no row to a 409.
//
// The document moved between the read and the write. Nothing failed — the guard is
// what stopped it — so this is a conflict rather than an error, and the caller can
// re-read and decide again.
func conflictOnTransisi(err error, message string) error {
	if errors.Is(err, repository.ErrTransisiStatus) {
		return model.Conflict(message)
	}

	return err
}

// nomorDokumen reserves the next number in a unit's series and formats it —
// isu #21 fase 1. idUnitKerja nil means the global series (a caller acting
// under a grant with no unit at all), formatted PREFIX/YYYY/MM/NNNN exactly as
// every number was before this issue. A real unit is formatted
// PREFIX/KODE/YYYY/MM/NNNN, and a unit with no kode is refused rather than
// silently falling back to its numeric id — a document number nobody can read
// is worse than one delayed until the unit is finished being set up. Checked
// before Next is ever called, so a missing kode never burns a number.
//
// The month comes from the document's own date, not from today, so an invoice dated
// in July gets a July number however late it is typed in — which is what makes a
// numbering series match the books it belongs to.
//
// Shared by every transaction document rather than reimplemented per module: two
// modules formatting a number two ways is how a numbering scheme quietly stops
// being one.
func nomorDokumen(ctx context.Context, tx repository.DBTX, counter *repository.DocumentCounterRepository, prefix string, tanggal time.Time, idUnitKerja *int64, kode *string) (string, error) {
	if idUnitKerja != nil && kode == nil {
		return "", model.Invalid(fmt.Sprintf(
			"unit_kerja (id %d) belum punya kode; lengkapi kode sebelum menerbitkan nomor dokumen di unit ini",
			*idUnitKerja,
		))
	}

	tahun, bulan := tanggal.Year(), int(tanggal.Month())

	urut, err := counter.Next(ctx, tx, prefix, tahun, bulan, idUnitKerja)
	if err != nil {
		return "", err
	}

	if idUnitKerja == nil {
		return fmt.Sprintf("%s/%04d/%02d/%04d", prefix, tahun, bulan, urut), nil
	}

	return fmt.Sprintf("%s/%s/%04d/%02d/%04d", prefix, *kode, tahun, bulan, urut), nil
}

// nomorDokumenUntukRuang resolves the unit_kerja a room belongs to and
// reserves the next number in that unit's series — the shape most numbered
// documents use (isu #21 fase 1). The unit is always read off the room the
// document actually belongs to, never the caller's active unit_kerja, so a
// number stays traceable to the outlet the goods belong to regardless of who
// happened to type the document. mutasi passes id_ruang_asal, never
// id_ruang_tujuan — the same source-only asymmetry fase 5 already applies to
// validation; penerimaan_susulan and retur_pembelian pass the parent
// pembelian's id_ruang, since they carry none of their own.
func nomorDokumenUntukRuang(ctx context.Context, tx repository.DBTX, counter *repository.DocumentCounterRepository, ruang *repository.RuangRepository, unitKerja *repository.UnitKerjaRepository, prefix string, tanggal time.Time, idRuang int64) (string, error) {
	idUnitKerja, err := ruang.IDUnitKerjaByID(ctx, tx, idRuang)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Let the document's own INSERT answer this with its usual
			// foreign-key message; an id_ruang this function cannot resolve
			// a unit for is not this function's failure to explain.
			return "", model.Invalid("id_ruang tidak ada")
		}

		return "", err
	}

	kode, err := unitKerja.KodeByID(ctx, tx, idUnitKerja)
	if err != nil {
		return "", err
	}

	return nomorDokumen(ctx, tx, counter, prefix, tanggal, &idUnitKerja, kode)
}

// nomorDokumenUntukUnitAktif is nomorDokumenUntukRuang's counterpart for the
// two documents with no room of their own — pembayaran_utang and
// penerimaan_pembayaran (isu #21 fase 1). There is no room to read a unit off,
// so the caller's own active unit_kerja is what the number is keyed to
// instead; nil (a global grant) falls back to the pre-existing global series,
// formatted exactly as it always has been.
func nomorDokumenUntukUnitAktif(ctx context.Context, tx repository.DBTX, counter *repository.DocumentCounterRepository, unitKerja *repository.UnitKerjaRepository, prefix string, tanggal time.Time, aktifIDUnitKerja *int64) (string, error) {
	if aktifIDUnitKerja == nil {
		return nomorDokumen(ctx, tx, counter, prefix, tanggal, nil, nil)
	}

	kode, err := unitKerja.KodeByID(ctx, tx, *aktifIDUnitKerja)
	if err != nil {
		return "", err
	}

	return nomorDokumen(ctx, tx, counter, prefix, tanggal, aktifIDUnitKerja, kode)
}

// pageMetadata builds the paging block. Call it only after
// PageRequest.Normalize, or Size may still be 0 and total_page divides by zero.
func pageMetadata(request *model.PageRequest, total int64) *model.PageMetadata {
	totalPage := total / int64(request.Size)
	if total%int64(request.Size) != 0 {
		totalPage++
	}

	return &model.PageMetadata{
		Page:      request.Page,
		Size:      request.Size,
		TotalItem: total,
		TotalPage: totalPage,
	}
}
