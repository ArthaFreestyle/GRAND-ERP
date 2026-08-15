package usecase

// Internal test, same reason as pembelian_alokasi_test.go: this is a pure function
// with no database in sight, and what it pins — which calendar date a UTC instant
// falls on in WIB — is exactly the timezone trap isu #8 fase 1 asks to be decided,
// commented, and tested at the midnight boundary.

import (
	"testing"
	"time"
)

func TestTanggalHargaJualMidnightBoundaryWIB(t *testing.T) {
	cases := []struct {
		name string
		utc  string
		want string
	}{
		{
			// 23:59:59 WIB on the 14th, one second before the boundary.
			name: "just before midnight WIB stays on the earlier date",
			utc:  "2026-08-14T16:59:59Z",
			want: "2026-08-14",
		},
		{
			// Exactly 00:00:00 WIB on the 15th — the boundary itself.
			name: "exactly midnight WIB rolls to the later date",
			utc:  "2026-08-14T17:00:00Z",
			want: "2026-08-15",
		},
		{
			// A UTC date that has not yet turned over in Jakarta: still the 14th
			// there even though a UTC-naive truncation would already say the 15th
			// is impossible here since WIB is ahead of UTC, so instead prove the
			// reverse case — late UTC evening still reads as the same WIB day.
			name: "UTC truncation would agree here, sanity check",
			utc:  "2026-08-14T00:00:00Z",
			want: "2026-08-14",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			instant, err := time.Parse(time.RFC3339, c.utc)
			if err != nil {
				t.Fatalf("parse fixture %q: %v", c.utc, err)
			}

			got := tanggalHargaJual(instant).Format(dateOnly)
			if got != c.want {
				t.Errorf("tanggalHargaJual(%s) = %s, want %s", c.utc, got, c.want)
			}
		})
	}
}
