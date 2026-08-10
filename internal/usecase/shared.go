package usecase

import (
	"database/sql"
	"errors"

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
