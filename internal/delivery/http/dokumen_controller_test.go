// Internal test package: dispositionLampiran is unexported, and exporting a helper
// purely to let a test at it would be the wrong trade. No database and no server —
// this is string handling, and the string happens to be a response header.
package http

import (
	"strings"
	"testing"
)

// nama_asli is stored exactly as the client sent it, because it is display text.
// That means arbitrary bytes reach the Content-Disposition header, and a CR or LF
// there is header injection: everything after it is read by the client as a header
// of its own, or as the start of the body.
//
// So the quoted form is reduced to safe ASCII and the real name travels in the RFC
// 5987 filename* parameter, which is percent-encoded and is what current browsers
// prefer anyway.
func TestDispositionLampiranTidakBisaDisuntikiHeader(t *testing.T) {
	for _, nama := range []string{
		"faktur.pdf",
		"faktur\r\nX-Injected: yes",
		`faktur";X-Injected="yes`,
		"faktur\x00.pdf",
		"faktur\\.pdf",
		"faktur ke-2 (asli).pdf",
		"фактура.pdf",
	} {
		got := dispositionLampiran(nama)

		if strings.ContainsAny(got, "\r\n\x00") {
			t.Errorf("dispositionLampiran(%q) = %q, contains a header separator", nama, got)
		}

		if !strings.HasPrefix(got, `attachment; filename="`) {
			t.Errorf("dispositionLampiran(%q) = %q, want it to start as an attachment", nama, got)
		}

		// Exactly three quotes: the pair around the ASCII fallback, and none escaping
		// out of it. filename* is unquoted by definition.
		if strings.Count(got, `"`) != 2 {
			t.Errorf("dispositionLampiran(%q) = %q, want exactly two quote characters", nama, got)
		}

		if !strings.Contains(got, `filename*=UTF-8''`) {
			t.Errorf("dispositionLampiran(%q) = %q, want an RFC 5987 filename*", nama, got)
		}
	}
}

// A file whose name is blank — or is nothing but whitespace, which a newline is —
// still has to download as something.
func TestDispositionLampiranMenggantiNamaKosong(t *testing.T) {
	for _, nama := range []string{"", "   ", "\r\n"} {
		got := dispositionLampiran(nama)

		if !strings.Contains(got, `filename="lampiran"`) {
			t.Errorf("dispositionLampiran(%q) = %q, want the fallback name", nama, got)
		}

		// Both forms fall back together; a filename* left empty is a malformed
		// parameter that some clients prefer over the one that is actually there.
		if !strings.HasSuffix(got, `filename*=UTF-8''lampiran`) {
			t.Errorf("dispositionLampiran(%q) = %q, want the fallback in filename* too", nama, got)
		}
	}
}
