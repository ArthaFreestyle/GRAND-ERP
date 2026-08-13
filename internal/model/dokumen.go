package model

import (
	"io"
	"time"
)

// DokumenResponse is one attachment's metadata. The bytes themselves are served by
// GET /api/v1/dokumen/{id}, never inlined here.
//
// mime and ukuran_byte are what the server measured, not what the client claimed:
// the type comes from sniffing the file's own bytes and the size from counting them
// as they were written.
type DokumenResponse struct {
	ID       int64  `json:"id"`
	NamaAsli string `json:"nama_asli"`
	Mime     string `json:"mime"`
	// UkuranByte is a number rather than a decimal string: it is a byte count, not
	// money, so a float64 represents it exactly well past any size this API accepts.
	UkuranByte     int64   `json:"ukuran_byte"`
	ChecksumSHA256 *string `json:"checksum_sha256"`

	// Both nil while the file is still an orphan. That is a normal state, not an
	// error: the photo is taken before the document it belongs to exists.
	RefTable *string `json:"ref_table"`
	RefID    *int64  `json:"ref_id"`

	CreatedBy int64 `json:"created_by"`
	// NamaPembuat is resolved by a join, not a second query.
	NamaPembuat *string    `json:"nama_pembuat,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	DeletedAt   *time.Time `json:"deleted_at"`

	// DuplikatDariID is filled by upload only, and only when a live attachment with
	// the same checksum already exists. Advisory: the upload succeeds either way,
	// because the same scan legitimately gets attached to two different documents.
	// It is here so the receiving screen can ask "you already uploaded this — did
	// you mean to?" before a second copy of one invoice enters the system.
	DuplikatDariID *int64 `json:"duplikat_dari_id,omitempty"`
}

// UploadDokumenRequest carries one multipart part.
//
// Berkas is an io.Reader rather than a *multipart.FileHeader on purpose: the usecase
// layer must not know it is being fed by an HTTP form. The controller opens the part
// and hands over the stream; a test hands over a bytes.Reader.
//
// UkuranDilaporkan is what the multipart header claims. It is used only to refuse an
// obviously oversized upload before a byte is written — the limit that actually holds
// is enforced while streaming, since a claimed size is as controllable as any other
// client-supplied value.
type UploadDokumenRequest struct {
	NamaAsli         string    `json:"nama_asli" validate:"required,max=255"`
	Berkas           io.Reader `json:"-" validate:"-"`
	UkuranDilaporkan int64     `json:"-"`
	ActorID          int64     `json:"-" validate:"required,gt=0"`
}

type GetDokumenRequest struct {
	ID      int64 `param:"id" validate:"required,gt=0"`
	ActorID int64 `json:"-" validate:"required,gt=0"`
}

// TempelDokumenRequest attaches an orphan to the document it belongs to.
//
// ref_table is checked against a whitelist rather than trusted: there is no foreign
// key behind a polymorphic reference, so nothing in the database would catch a
// typo — or a caller pointing an attachment at a table that is not a document at
// all.
type TempelDokumenRequest struct {
	ID       int64  `json:"-" validate:"required,gt=0"`
	RefTable string `json:"ref_table" validate:"required,max=64"`
	RefID    int64  `json:"ref_id" validate:"required,gt=0"`
	ActorID  int64  `json:"-" validate:"required,gt=0"`
}

// ListDokumenRequest lists one document's attachments, or — with neither ref
// supplied — the caller's own orphans, which is what a receiving screen needs to
// offer the photos taken minutes ago.
//
// ref_table and ref_id are both-or-neither: half a reference names either a whole
// table or nothing at all.
type ListDokumenRequest struct {
	PageRequest
	RefTable string `query:"ref_table" validate:"omitempty,max=64"`
	RefID    *int64 `query:"ref_id" validate:"omitempty,gt=0"`
	ActorID  int64  `json:"-" validate:"required,gt=0"`
}

type DeleteDokumenRequest struct {
	ID      int64 `param:"id" validate:"required,gt=0"`
	ActorID int64 `json:"-" validate:"required,gt=0"`
}
