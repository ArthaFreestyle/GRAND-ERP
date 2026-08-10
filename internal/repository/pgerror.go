package repository

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// uniqueViolation is PostgreSQL's SQLSTATE for a unique constraint or unique
// index violation.
const uniqueViolation = "23505"

// foreignKeyViolation is PostgreSQL's SQLSTATE for a referenced row that is not
// there.
const foreignKeyViolation = "23503"

// exclusionViolation is PostgreSQL's SQLSTATE for an EXCLUDE constraint. The only one
// in this schema is product_harga_jual_no_overlap, which stops two price versions
// from being valid at once for the same product and unit.
const exclusionViolation = "23P01"

// UniqueViolation reports whether err is a unique-constraint violation and, if
// so, which constraint or index rejected the row.
//
// Checking ExistsByKode before INSERT is not a guarantee: two concurrent
// requests can both pass the check, and one then loses at the database. Without
// this mapping that loser becomes a 500 instead of a 409. The pre-check stays
// only so the common case gets a friendly message naming the field.
//
// The constraint name is returned so a usecase with more than one unique index
// on a table can tell the caller which value collided.
func UniqueViolation(err error) (string, bool) {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != uniqueViolation {
		return "", false
	}

	return pgErr.ConstraintName, true
}

// IsUniqueViolation is UniqueViolation for callers that do not care which
// constraint was hit.
func IsUniqueViolation(err error) bool {
	_, ok := UniqueViolation(err)

	return ok
}

// IsExclusionViolation reports whether err is an EXCLUDE constraint violation.
//
// A GiST exclusion constraint is the only thing that can catch two overlapping price
// ranges: the check happens across rows, so no per-row CHECK and no application
// pre-check can be trusted with it. Without this mapping an overlapping price version
// surfaces as a 500 instead of a 409.
func IsExclusionViolation(err error) bool {
	var pgErr *pgconn.PgError

	return errors.As(err, &pgErr) && pgErr.Code == exclusionViolation
}

// IsForeignKeyViolation reports whether err is a foreign-key violation.
//
// It plays the same role for references that IsUniqueViolation plays for
// duplicates. Checking that every role id exists before granting it is not a
// guarantee either: a role can be deleted between the check and the INSERT into
// user_role, and without this mapping the loser of that race becomes a 500 instead
// of a 400 telling the caller the role is gone.
func IsForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError

	return errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation
}
