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

// nomorDokumen reserves the next number in a series and formats it as
// PREFIX/YYYY/MM/NNNN.
//
// The month comes from the document's own date, not from today, so an invoice dated
// in July gets a July number however late it is typed in — which is what makes a
// numbering series match the books it belongs to.
//
// Shared by every transaction document rather than reimplemented per module: two
// modules formatting a number two ways is how a numbering scheme quietly stops
// being one.
func nomorDokumen(ctx context.Context, tx repository.DBTX, counter *repository.DocumentCounterRepository, prefix string, tanggal time.Time) (string, error) {
	tahun, bulan := tanggal.Year(), int(tanggal.Month())

	urut, err := counter.Next(ctx, tx, prefix, tahun, bulan)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s/%04d/%02d/%04d", prefix, tahun, bulan, urut), nil
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
