package repository

import (
	"context"
	"fmt"
)

// Document number prefixes, one per transaction table. Kept together so the set of
// numbering series a deployment has is readable in one place, and so two modules
// cannot accidentally share a counter by both typing the same literal.
const (
	PrefixPembelian       = "BL"
	PrefixSusulan         = "PS"
	PrefixRetur           = "RB"
	PrefixPembayaranUtang = "PU"
)

// DocumentCounterRepository hands out the per-month sequence behind every document
// number.
//
// Every transaction table declares `nomor VARCHAR NOT NULL UNIQUE` and until
// migration 000012 nothing produced one. This is that generator, and it is shared:
// penjualan, mutasi, and the payment documents all need the same thing, so it is
// keyed by a prefix rather than built into one module.
type DocumentCounterRepository struct{}

func NewDocumentCounterRepository() *DocumentCounterRepository {
	return &DocumentCounterRepository{}
}

// Next reserves and returns the next sequence number for a prefix and month.
//
// One statement, and that is the whole concurrency argument. INSERT ... ON CONFLICT
// DO UPDATE takes a row lock on the counter and holds it until the surrounding
// transaction ends, so a second request for the same series blocks rather than
// reading the same last_number. A SELECT-then-UPDATE pair could not promise that,
// and neither could a check in Go.
//
// Call it inside the document's own transaction. A rollback then gives the number
// back to nobody — the counter stays advanced and that number is never used. Gaps
// in a numbering series are an accounting annoyance; two documents sharing a number
// is a corruption, so the trade goes this way deliberately.
func (r *DocumentCounterRepository) Next(ctx context.Context, db DBTX, prefix string, tahun, bulan int) (int64, error) {
	const query = `
		INSERT INTO document_counter (prefix, tahun, bulan, last_number)
		VALUES ($1, $2, $3, 1)
		ON CONFLICT (prefix, tahun, bulan) DO UPDATE
			SET last_number = document_counter.last_number + 1,
			    updated_at  = now()
		RETURNING last_number`

	var urut int64
	if err := db.QueryRowContext(ctx, query, prefix, tahun, bulan).Scan(&urut); err != nil {
		return 0, fmt.Errorf("next document_counter %s: %w", prefix, err)
	}

	return urut, nil
}
