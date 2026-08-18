package usecase_test

// These run against a real PostgreSQL and a real directory, because both halves of
// this module are the point: a row and a file have to end up agreeing, and a mock of
// either would agree with whatever the code did. What is asserted here is mostly
// what happens on disk — that the client's filename never became a path, that a file
// over the limit left nothing behind, that a swept orphan is gone from the directory
// and not merely marked in the table.

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"Arthafreestyle/ERP/internal/config"
	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/repository"
	"Arthafreestyle/ERP/internal/usecase"

	"github.com/sirupsen/logrus"
)

// Magic bytes, because the type is decided by the file's own content and nothing
// else. Each is the prefix http.DetectContentType matches on, padded so the sniff
// buffer has something to read past it.
var (
	isiPNG  = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0x00}, 64)...)
	isiJPEG = append([]byte("\xFF\xD8\xFF\xE0"), bytes.Repeat([]byte{0x00}, 64)...)
	isiPDF  = append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte{0x20}, 64)...)
	isiHTML = []byte("<!DOCTYPE html><html><body><script>alert(1)</script></body></html>")
)

// dokumenFixture creates the one thing an upload needs: an actor, since created_by
// is NOT NULL.
func dokumenFixture(t *testing.T, testApp *app) int64 {
	t.Helper()

	user, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		ActorID:  testActor(t),
		Username: "petugas_unggah",
		Password: "rahasia123",
	})
	if err != nil {
		t.Fatalf("create actor: %v", err)
	}

	return user.ID
}

func unggah(t *testing.T, testApp *app, actor int64, nama string, isi []byte) *model.DokumenResponse {
	t.Helper()

	response, err := testApp.dokumen.Upload(ctx(), &model.UploadDokumenRequest{
		NamaAsli:         nama,
		Berkas:           bytes.NewReader(isi),
		UkuranDilaporkan: int64(len(isi)),
		ActorID:          actor,
	})
	if err != nil {
		t.Fatalf("upload %q: %v", nama, err)
	}

	return response
}

// berkasTersimpan lists what is actually in the storage directory. Half of what this
// module promises is about files, and only the filesystem can be asked about those.
func berkasTersimpan(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read storage dir: %v", err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	return names
}

// dokumenDenganTTL builds a second usecase over the same storage directory, differing
// only in how old an orphan must be before it is swept. The cleanup rule is a
// comparison against created_at, so the only way to test both sides of it without
// sleeping is to move the threshold rather than the rows.
func dokumenDenganTTL(t *testing.T, testApp *app, ttl time.Duration) *usecase.DokumenUseCase {
	t.Helper()

	storage, err := repository.NewLocalDokumenStorage(testApp.dokumenDir)
	if err != nil {
		t.Fatalf("dokumen storage: %v", err)
	}

	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	return usecase.NewDokumenUseCase(
		testDB, log, config.NewValidator(), repository.NewDokumenRepository(), storage,
		testMaxUkuranDokumen, ttl,
	)
}

// The MIME type is decided by the bytes, never by what the request claims. A page of
// HTML announced as a PDF is the exact trick the sniffing exists to defeat, and
// honouring the claim would put a script into the application's own origin the
// moment somebody opened the "invoice".
func TestUploadMenentukanMimeDariIsiBukanNamaAtauHeader(t *testing.T) {
	testApp := newApp(t)
	actor := dokumenFixture(t, testApp)

	_, err := testApp.dokumen.Upload(ctx(), &model.UploadDokumenRequest{
		NamaAsli:         "faktur.pdf",
		Berkas:           bytes.NewReader(isiHTML),
		UkuranDilaporkan: int64(len(isiHTML)),
		ActorID:          actor,
	})
	assertKind(t, err, model.KindInvalid)

	// Rejected before anything was written: the sniff happens on the first 512 bytes,
	// which is why a refused type costs no disk at all.
	if names := berkasTersimpan(t, testApp.dokumenDir); len(names) != 0 {
		t.Errorf("storage dir = %v, want empty", names)
	}
}

// Every accepted type gets its extension from the detected MIME, not from the name
// the client sent.
func TestUploadMenurunkanEkstensiDariMimeHasilDeteksi(t *testing.T) {
	testApp := newApp(t)
	actor := dokumenFixture(t, testApp)

	for _, kasus := range []struct {
		nama     string
		isi      []byte
		mime     string
		ekstensi string
	}{
		{"foto.txt", isiPNG, "image/png", ".png"},
		{"scan.bin", isiJPEG, "image/jpeg", ".jpg"},
		{"nota.png", isiPDF, "application/pdf", ".pdf"},
	} {
		response := unggah(t, testApp, actor, kasus.nama, kasus.isi)

		if response.Mime != kasus.mime {
			t.Errorf("%s: mime = %q, want %q", kasus.nama, response.Mime, kasus.mime)
		}

		if response.NamaAsli != kasus.nama {
			t.Errorf("%s: nama_asli = %q, want it kept verbatim", kasus.nama, response.NamaAsli)
		}
	}

	for _, nama := range berkasTersimpan(t, testApp.dokumenDir) {
		if !strings.HasSuffix(nama, ".png") && !strings.HasSuffix(nama, ".jpg") &&
			!strings.HasSuffix(nama, ".pdf") {
			t.Errorf("stored name %q does not carry a sniffed extension", nama)
		}
	}
}

// The one rule that makes every other one safe: the client's filename is display
// text and never a path. Without it, "../../config.json" is a legal place to write.
func TestUploadTidakPernahMemakaiNamaKlienSebagaiPath(t *testing.T) {
	testApp := newApp(t)
	actor := dokumenFixture(t, testApp)

	response := unggah(t, testApp, actor, "../../config.json", isiPNG)

	if response.NamaAsli != "../../config.json" {
		t.Errorf("nama_asli = %q, want it kept for display", response.NamaAsli)
	}

	names := berkasTersimpan(t, testApp.dokumenDir)
	if len(names) != 1 {
		t.Fatalf("storage dir = %v, want exactly one file", names)
	}

	if strings.Contains(names[0], "config") || strings.ContainsAny(names[0], `/\`) {
		t.Errorf("stored name = %q, want a generated name with no path in it", names[0])
	}

	if !strings.HasSuffix(names[0], ".png") {
		t.Errorf("stored name = %q, want a .png extension from the sniffed type", names[0])
	}
}

// The limit is enforced while streaming, so the rejection must also take the partial
// file with it. A refused upload that leaves bytes behind is a leak nothing can find
// later: the cleanup job works from rows, and this one never got a row.
func TestUploadMenolakBerkasMelebihiBatasDanTidakMeninggalkanBerkas(t *testing.T) {
	testApp := newApp(t)
	actor := dokumenFixture(t, testApp)

	besar := append(append([]byte{}, isiPNG...), bytes.Repeat([]byte{0x41}, testMaxUkuranDokumen)...)

	// UkuranDilaporkan is left at zero on purpose: it is client-supplied, so a caller
	// that lies about the size must still be stopped by the stream limit rather than
	// by the cheap pre-check.
	_, err := testApp.dokumen.Upload(ctx(), &model.UploadDokumenRequest{
		NamaAsli: "besar.png",
		Berkas:   bytes.NewReader(besar),
		ActorID:  actor,
	})
	assertKind(t, err, model.KindInvalid)

	if names := berkasTersimpan(t, testApp.dokumenDir); len(names) != 0 {
		t.Errorf("storage dir = %v, want empty after an oversized upload", names)
	}
}

func TestUploadMenolakBerkasKosong(t *testing.T) {
	testApp := newApp(t)
	actor := dokumenFixture(t, testApp)

	_, err := testApp.dokumen.Upload(ctx(), &model.UploadDokumenRequest{
		NamaAsli: "kosong.png",
		Berkas:   bytes.NewReader(nil),
		ActorID:  actor,
	})
	assertKind(t, err, model.KindInvalid)
}

// The bytes that come back are the bytes that went in — the sniff buffer is read
// ahead of the copy and has to be put back in front of it, which is the one place a
// file could silently lose its first 512 bytes.
func TestIsiMengembalikanBerkasUtuh(t *testing.T) {
	testApp := newApp(t)
	actor := dokumenFixture(t, testApp)

	asli := append(append([]byte{}, isiPDF...), bytes.Repeat([]byte("faktur"), 500)...)
	dokumen := unggah(t, testApp, actor, "faktur.pdf", asli)

	if dokumen.UkuranByte != int64(len(asli)) {
		t.Errorf("ukuran_byte = %d, want %d", dokumen.UkuranByte, len(asli))
	}

	_, berkas, err := testApp.dokumen.Isi(ctx(), &model.GetDokumenRequest{ID: dokumen.ID, ActorID: actor})
	if err != nil {
		t.Fatalf("isi: %v", err)
	}
	defer func() {
		_ = berkas.Close()
	}()

	isi, err := io.ReadAll(berkas)
	if err != nil {
		t.Fatalf("read isi: %v", err)
	}

	if !bytes.Equal(isi, asli) {
		t.Errorf("isi berkas berbeda: %d byte kembali, %d byte dikirim", len(isi), len(asli))
	}
}

// The same invoice photographed twice is a mistake worth showing, but never a reason
// to refuse: one scan legitimately belongs to two different documents.
func TestUploadMelaporkanDuplikatTanpaMenolaknya(t *testing.T) {
	testApp := newApp(t)
	actor := dokumenFixture(t, testApp)

	pertama := unggah(t, testApp, actor, "faktur.png", isiPNG)
	kedua := unggah(t, testApp, actor, "faktur-lagi.png", isiPNG)

	if kedua.DuplikatDariID == nil {
		t.Fatalf("duplikat_dari_id = nil, want %d", pertama.ID)
	}

	if *kedua.DuplikatDariID != pertama.ID {
		t.Errorf("duplikat_dari_id = %d, want %d", *kedua.DuplikatDariID, pertama.ID)
	}

	if pertama.ChecksumSHA256 == nil || kedua.ChecksumSHA256 == nil ||
		*pertama.ChecksumSHA256 != *kedua.ChecksumSHA256 {
		t.Errorf("checksums differ for identical content")
	}
}

// Upload first, attach later — the whole shape of the module. An upload is born an
// orphan because the document it belongs to usually does not exist yet.
func TestTempelMengisiReferensiDanMenolakYangKedua(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)
	pembelian := draftSederhana(t, testApp, f, "10", nil, nil)

	dokumen := unggah(t, testApp, f.actor, "faktur.png", isiPNG)

	if dokumen.RefTable != nil || dokumen.RefID != nil {
		t.Fatalf("upload arrived attached: ref_table=%v ref_id=%v", dokumen.RefTable, dokumen.RefID)
	}

	tertempel, err := testApp.dokumen.Tempel(ctx(), &model.TempelDokumenRequest{
		ID:       dokumen.ID,
		RefTable: "pembelian",
		RefID:    pembelian.ID,
		ActorID:  f.actor,
	})
	if err != nil {
		t.Fatalf("tempel: %v", err)
	}

	if tertempel.RefTable == nil || *tertempel.RefTable != "pembelian" ||
		tertempel.RefID == nil || *tertempel.RefID != pembelian.ID {
		t.Fatalf("ref = %v/%v, want pembelian/%d", tertempel.RefTable, tertempel.RefID, pembelian.ID)
	}

	// Attaching an already-attached file would move it, silently taking evidence off
	// the document that had it.
	kedua := draftSederhana(t, testApp, f, "5", nil, nil)

	_, err = testApp.dokumen.Tempel(ctx(), &model.TempelDokumenRequest{
		ID:       dokumen.ID,
		RefTable: "pembelian",
		RefID:    kedua.ID,
		ActorID:  f.actor,
	})
	assertKind(t, err, model.KindConflict)
}

// There is no foreign key behind a polymorphic reference, so the whitelist in the
// repository is the only thing that says which strings name a document at all.
// "users" is a real table and still not an answer.
func TestTempelMenolakRefTableDiLuarWhitelist(t *testing.T) {
	testApp := newApp(t)
	actor := dokumenFixture(t, testApp)

	dokumen := unggah(t, testApp, actor, "faktur.png", isiPNG)

	_, err := testApp.dokumen.Tempel(ctx(), &model.TempelDokumenRequest{
		ID:       dokumen.ID,
		RefTable: "users",
		RefID:    actor,
		ActorID:  actor,
	})
	assertKind(t, err, model.KindInvalid)
}

// A ref_id naming no document is the same class of mistake as a foreign key that
// does not resolve, and gets the same 400 — not a 404, which would claim the
// attachment itself is missing.
func TestTempelMenolakIndukYangTidakAda(t *testing.T) {
	testApp := newApp(t)
	actor := dokumenFixture(t, testApp)

	dokumen := unggah(t, testApp, actor, "faktur.png", isiPNG)

	_, err := testApp.dokumen.Tempel(ctx(), &model.TempelDokumenRequest{
		ID:       dokumen.ID,
		RefTable: "pembelian",
		RefID:    999_999,
		ActorID:  actor,
	})
	assertKind(t, err, model.KindInvalid)
}

// Ten is enough for a multi-page invoice photographed one page at a time; past that
// it is a stuck retry loop, and one document would otherwise mean unbounded disk.
func TestTempelMenolakLebihDariBatasLampiran(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)
	pembelian := draftSederhana(t, testApp, f, "10", nil, nil)

	// The eleventh is the one that must fail; the first ten are the case this limit
	// has to keep working.
	for i := range 11 {
		// Distinct content per file, so the duplicate lookup is not what is being
		// measured here.
		isi := append(append([]byte{}, isiPNG...), byte(i))

		dokumen, err := testApp.dokumen.Upload(ctx(), &model.UploadDokumenRequest{
			NamaAsli: "halaman.png",
			Berkas:   bytes.NewReader(isi),
			ActorID:  f.actor,
		})
		if err != nil {
			t.Fatalf("upload %d: %v", i, err)
		}

		_, err = testApp.dokumen.Tempel(ctx(), &model.TempelDokumenRequest{
			ID:       dokumen.ID,
			RefTable: "pembelian",
			RefID:    pembelian.ID,
			ActorID:  f.actor,
		})

		if i < 10 {
			if err != nil {
				t.Fatalf("tempel %d: %v", i, err)
			}

			continue
		}

		assertKind(t, err, model.KindConflict)
	}
}

// Soft delete: the file goes, the row stays. The trace of an upload outliving its
// bytes is what makes the cleanup job re-runnable and what keeps "somebody uploaded
// something here" answerable.
func TestHapusMenghilangkanBerkasTetapiMenyisakanBaris(t *testing.T) {
	testApp := newApp(t)
	actor := dokumenFixture(t, testApp)

	dokumen := unggah(t, testApp, actor, "faktur.png", isiPNG)

	if names := berkasTersimpan(t, testApp.dokumenDir); len(names) != 1 {
		t.Fatalf("storage dir = %v, want exactly one file", names)
	}

	dihapus, err := testApp.dokumen.Hapus(ctx(), &model.DeleteDokumenRequest{
		ID: dokumen.ID, ActorID: actor,
	})
	if err != nil {
		t.Fatalf("hapus: %v", err)
	}

	// The response confirming a deletion has to say the row is deleted. It is built
	// from the snapshot read before the lock, so the mark has to be carried onto it.
	if dihapus.DeletedAt == nil {
		t.Error("hapus response deleted_at = nil, want the moment it was deleted")
	}

	if names := berkasTersimpan(t, testApp.dokumenDir); len(names) != 0 {
		t.Errorf("storage dir = %v, want empty after delete", names)
	}

	// The row is still there, but a deleted attachment has nothing to serve.
	_, _, err = testApp.dokumen.Isi(ctx(), &model.GetDokumenRequest{ID: dokumen.ID, ActorID: actor})
	assertKind(t, err, model.KindNotFound)

	_, err = testApp.dokumen.Hapus(ctx(), &model.DeleteDokumenRequest{ID: dokumen.ID, ActorID: actor})
	assertKind(t, err, model.KindConflict)
}

// The removal rule in one test: an attachment may be taken back while its document is
// still a DRAFT, and not once that document has been submitted. A photograph of the
// invoice a purchase was approved against is part of the record by then.
func TestHapusHanyaSaatYatimAtauIndukMasihDraft(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)
	pembelian := draftSederhana(t, testApp, f, "10", nil, nil)

	tempel := func(isi []byte) int64 {
		t.Helper()

		dokumen, err := testApp.dokumen.Upload(ctx(), &model.UploadDokumenRequest{
			NamaAsli: "faktur.png",
			Berkas:   bytes.NewReader(isi),
			ActorID:  f.actor,
		})
		if err != nil {
			t.Fatalf("upload: %v", err)
		}

		if _, err := testApp.dokumen.Tempel(ctx(), &model.TempelDokumenRequest{
			ID:       dokumen.ID,
			RefTable: "pembelian",
			RefID:    pembelian.ID,
			ActorID:  f.actor,
		}); err != nil {
			t.Fatalf("tempel: %v", err)
		}

		return dokumen.ID
	}

	padaDraft := tempel(append(append([]byte{}, isiPNG...), 'a'))

	if _, err := testApp.dokumen.Hapus(ctx(), &model.DeleteDokumenRequest{
		ID: padaDraft, ActorID: f.actor,
	}); err != nil {
		t.Fatalf("hapus lampiran DRAFT: %v", err)
	}

	setelahDiajukan := tempel(append(append([]byte{}, isiPNG...), 'b'))

	if _, err := testApp.pembelian.Ajukan(ctx(), &model.AjukanPembelianRequest{
		ID: pembelian.ID, ActorID: f.actor,
	}); err != nil {
		t.Fatalf("ajukan pembelian: %v", err)
	}

	_, err := testApp.dokumen.Hapus(ctx(), &model.DeleteDokumenRequest{
		ID: setelahDiajukan, ActorID: f.actor,
	})
	assertKind(t, err, model.KindConflict)

	// And the file is still there: a refused delete must not have removed anything on
	// its way to saying no.
	if names := berkasTersimpan(t, testApp.dokumenDir); len(names) != 1 {
		t.Errorf("storage dir = %v, want the attachment still stored", names)
	}
}

// An attachment on a voided document could never be removed again — the removal rule
// only lets go of a DRAFT parent — so it is refused rather than made permanent on a
// document that no longer counts.
func TestTempelMenolakIndukYangSudahDibatalkan(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)
	pembelian := draftSederhana(t, testApp, f, "10", nil, nil)

	// Only a POSTED purchase can be voided, so it goes the whole way round first.
	if _, err := testApp.pembelian.Ajukan(ctx(), &model.AjukanPembelianRequest{
		ID: pembelian.ID, ActorID: f.actor,
	}); err != nil {
		t.Fatalf("ajukan pembelian: %v", err)
	}

	if _, err := testApp.pembelian.Posting(ctx(), &model.PostingPembelianRequest{
		ID: pembelian.ID, ActorID: f.actor,
	}); err != nil {
		t.Fatalf("posting pembelian: %v", err)
	}

	if _, err := testApp.pembelian.Batal(ctx(), &model.BatalPembelianRequest{
		ID: pembelian.ID, ActorID: f.actor, AlasanBatal: "salah supplier",
	}); err != nil {
		t.Fatalf("batal pembelian: %v", err)
	}

	dokumen := unggah(t, testApp, f.actor, "faktur.png", isiPNG)

	_, err := testApp.dokumen.Tempel(ctx(), &model.TempelDokumenRequest{
		ID:       dokumen.ID,
		RefTable: "pembelian",
		RefID:    pembelian.ID,
		ActorID:  f.actor,
	})
	assertKind(t, err, model.KindConflict)
}

// The cleanup job's whole contract: orphans older than the TTL go, everything else
// stays. Both halves matter — a sweep that also took attached files would be
// deleting evidence, and one that spared old orphans would never reclaim anything.
func TestBersihkanYatimHanyaMenyapuYangKedaluwarsaDanBelumTertempel(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)
	pembelian := draftSederhana(t, testApp, f, "10", nil, nil)

	tertempel := unggah(t, testApp, f.actor, "faktur.png", append(append([]byte{}, isiPNG...), 'a'))
	if _, err := testApp.dokumen.Tempel(ctx(), &model.TempelDokumenRequest{
		ID:       tertempel.ID,
		RefTable: "pembelian",
		RefID:    pembelian.ID,
		ActorID:  f.actor,
	}); err != nil {
		t.Fatalf("tempel: %v", err)
	}

	yatim := unggah(t, testApp, f.actor, "buram.png", append(append([]byte{}, isiPNG...), 'b'))

	// With the production TTL nothing is old enough yet, which is the case that keeps
	// a form left open over lunch from losing its photos.
	dibersihkan, err := testApp.dokumen.BersihkanYatim(ctx())
	if err != nil {
		t.Fatalf("bersihkan (TTL penuh): %v", err)
	}

	if dibersihkan != 0 {
		t.Errorf("dibersihkan = %d, want 0 while nothing has expired", dibersihkan)
	}

	if names := berkasTersimpan(t, testApp.dokumenDir); len(names) != 2 {
		t.Fatalf("storage dir = %v, want both files still stored", names)
	}

	// Moving the threshold rather than the clock: with a zero TTL every existing
	// orphan is expired, and the attached one still must not be touched.
	dibersihkan, err = dokumenDenganTTL(t, testApp, 0).BersihkanYatim(ctx())
	if err != nil {
		t.Fatalf("bersihkan (TTL nol): %v", err)
	}

	if dibersihkan != 1 {
		t.Errorf("dibersihkan = %d, want 1", dibersihkan)
	}

	if names := berkasTersimpan(t, testApp.dokumenDir); len(names) != 1 {
		t.Errorf("storage dir = %v, want only the attached file left", names)
	}

	// The swept row survives with deleted_at set, so its download is gone but its
	// trace is not.
	_, _, err = testApp.dokumen.Isi(ctx(), &model.GetDokumenRequest{ID: yatim.ID, ActorID: f.actor})
	assertKind(t, err, model.KindNotFound)

	_, masih, err := testApp.dokumen.Isi(ctx(), &model.GetDokumenRequest{
		ID: tertempel.ID, ActorID: f.actor,
	})
	if err != nil {
		t.Fatalf("attached dokumen was swept: %v", err)
	}

	// Closed rather than dropped: on Windows an open handle stops t.TempDir from
	// removing the directory, and the test fails in its own cleanup.
	_ = masih.Close()
}

// A second sweep must find nothing left to do. The job is re-run daily forever, and
// a count that keeps climbing over rows it already handled would mean it is deleting
// files it does not own.
func TestBersihkanYatimIdempoten(t *testing.T) {
	testApp := newApp(t)
	actor := dokumenFixture(t, testApp)

	unggah(t, testApp, actor, "buram.png", isiPNG)

	pembersih := dokumenDenganTTL(t, testApp, 0)

	pertama, err := pembersih.BersihkanYatim(ctx())
	if err != nil {
		t.Fatalf("bersihkan pertama: %v", err)
	}

	if pertama != 1 {
		t.Fatalf("dibersihkan pertama = %d, want 1", pertama)
	}

	kedua, err := pembersih.BersihkanYatim(ctx())
	if err != nil {
		t.Fatalf("bersihkan kedua: %v", err)
	}

	if kedua != 0 {
		t.Errorf("dibersihkan kedua = %d, want 0", kedua)
	}
}

// Two modes of one endpoint: a document's attachments, or the caller's own orphans.
// Half a reference is neither, and is refused rather than quietly listing everything.
func TestSearchDokumenDuaModeDanMenolakReferensiSetengah(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)
	pembelian := draftSederhana(t, testApp, f, "10", nil, nil)

	tertempel := unggah(t, testApp, f.actor, "faktur.png", append(append([]byte{}, isiPNG...), 'a'))
	if _, err := testApp.dokumen.Tempel(ctx(), &model.TempelDokumenRequest{
		ID:       tertempel.ID,
		RefTable: "pembelian",
		RefID:    pembelian.ID,
		ActorID:  f.actor,
	}); err != nil {
		t.Fatalf("tempel: %v", err)
	}

	unggah(t, testApp, f.actor, "belum.png", append(append([]byte{}, isiPNG...), 'b'))

	lampiran, paging, err := testApp.dokumen.Search(ctx(), &model.ListDokumenRequest{
		RefTable: "pembelian",
		RefID:    &pembelian.ID,
		ActorID:  f.actor,
	})
	if err != nil {
		t.Fatalf("search lampiran: %v", err)
	}

	if paging.TotalItem != 1 || len(lampiran) != 1 || lampiran[0].ID != tertempel.ID {
		t.Errorf("lampiran = %v (total %d), want only %d", lampiran, paging.TotalItem, tertempel.ID)
	}

	yatim, paging, err := testApp.dokumen.Search(ctx(), &model.ListDokumenRequest{ActorID: f.actor})
	if err != nil {
		t.Fatalf("search yatim: %v", err)
	}

	if paging.TotalItem != 1 || len(yatim) != 1 || yatim[0].RefID != nil {
		t.Errorf("yatim = %v (total %d), want exactly one unattached", yatim, paging.TotalItem)
	}

	_, _, err = testApp.dokumen.Search(ctx(), &model.ListDokumenRequest{
		RefTable: "pembelian",
		ActorID:  f.actor,
	})
	assertKind(t, err, model.KindInvalid)
}
