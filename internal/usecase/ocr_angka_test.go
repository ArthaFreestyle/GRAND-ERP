package usecase

import "testing"

func TestParseAngkaIndonesia(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "plain integer", input: "52000", want: "52000"},
		{name: "plain decimal already normalized", input: "1254000.00", want: "1254000.00"},
		{name: "thousands dot decimal comma", input: "1.254.000,00", want: "1254000.00"},
		{name: "thousands dot only, no decimal", input: "1.254.000", want: "1254000"},
		{name: "single comma as decimal separator", input: "10,5", want: "10.5"},
		{name: "multiple commas as thousands", input: "1,254,000", want: "1254000"},
		{name: "english style thousands comma decimal dot", input: "1,254,000.50", want: "1254000.50"},
		{name: "single dot with three digits reads as thousands", input: "10.500", want: "10500"},
		{name: "single dot with two digits reads as decimal", input: "99.99", want: "99.99"},
		{name: "single dot with one digit reads as decimal", input: "10.5", want: "10.5"},
		{name: "rp prefix and spaces", input: "Rp 1.254.000,00", want: "1254000.00"},
		{name: "negative", input: "-10.500", want: "-10500"},
		{name: "leading plus", input: "+10", want: "10"},
		{name: "whitespace padded", input: "  52000  ", want: "52000"},
		{name: "empty is an error", input: "", wantErr: true},
		{name: "not a number is an error", input: "abc", wantErr: true},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAngkaIndonesia(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseAngkaIndonesia(%q) = %v, want error", tt.input, got)
				}

				return
			}

			if err != nil {
				t.Fatalf("parseAngkaIndonesia(%q) unexpected error: %v", tt.input, err)
			}

			want, err := parseNumeric(tt.want)
			if err != nil {
				t.Fatalf("test case %q has an invalid want value %q: %v", tt.name, tt.want, err)
			}

			if got.Cmp(want) != 0 {
				t.Fatalf("parseAngkaIndonesia(%q) = %s, want %s", tt.input, got.RatString(), want.RatString())
			}
		})
	}
}

func TestParseUangIndonesia(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "plain integer", input: "5000", want: "5000"},
		{name: "single dot is thousands", input: "5.000", want: "5000"},
		{name: "multiple dots", input: "1.254.000", want: "1254000"},
		{name: "rp prefix", input: "Rp 1.254.000", want: "1254000"},
		{name: "dot decimal comma zero", input: "1.254.000,00", want: "1254000"},
		{name: "normalized dot zero zero", input: "5000.00", want: "5000"},
		{name: "single dot two digits is still thousands", input: "5.50", want: "550"},
		{name: "single dot one digit is still thousands", input: "5.5", want: "55"},
		{name: "decimal comma kept", input: "1.254.000,50", want: "1254000.50"},
		{name: "commas as grouping", input: "1,254,000", want: "1254000"},
		{name: "negative", input: "-5.000", want: "-5000"},
		{name: "empty is an error", input: "", wantErr: true},
		{name: "not a number is an error", input: "abc", wantErr: true},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseUangIndonesia(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseUangIndonesia(%q) = %v, want error", tt.input, got)
				}

				return
			}

			if err != nil {
				t.Fatalf("parseUangIndonesia(%q) unexpected error: %v", tt.input, err)
			}

			want, err := parseNumeric(tt.want)
			if err != nil {
				t.Fatalf("invalid want %q: %v", tt.want, err)
			}

			if got.Cmp(want) != 0 {
				t.Fatalf("parseUangIndonesia(%q) = %s, want %s", tt.input, got.RatString(), want.RatString())
			}
		})
	}
}
