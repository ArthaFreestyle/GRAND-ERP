package entity

import "time"

// Dokumen maps the dokumen table: one uploaded file, and where it is attached.
//
// The reference is polymorphic (ref_table + ref_id), the same shape kartu_stok
// uses, because attachments are infrastructure rather than a column of any one
// module. Both halves are nil while the file is still an orphan — uploaded, not
// yet attached to anything — which is the normal state for the seconds or minutes
// between photographing an invoice and saving the purchase it belongs to.
//
// PathSimpan is a bare filename, never a path. The directory it lives in is
// configuration (dokumen.storage_path), so moving the storage root does not mean
// rewriting every row.
type Dokumen struct {
	ID int64

	// NamaAsli is what the client called the file. It is display text and nothing
	// else: it never reaches the filesystem, so a name like "../../config.json" is
	// harmless here.
	NamaAsli string
	// PathSimpan is the server-generated name — a UUID plus an extension derived
	// from the sniffed MIME type.
	PathSimpan string
	// Mime is detected from the file's own bytes, not from the Content-Type header
	// the client sent.
	Mime           string
	UkuranByte     int64
	ChecksumSHA256 *string

	RefTable *string
	RefID    *int64

	CreatedBy int64
	CreatedAt time.Time
	// DeletedAt set means the file is already gone from disk; only the trace of the
	// upload survives.
	DeletedAt *time.Time

	// NamaPembuat is not a column of dokumen. It comes from a LEFT JOIN on users and
	// is only filled by the read queries — resolving it per row would be an N+1.
	NamaPembuat *string
}
