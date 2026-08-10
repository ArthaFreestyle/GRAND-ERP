package repository

import "testing"

func TestEscapeLike(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "plain text is untouched", input: "Gudang Utama", want: "Gudang Utama"},
		{name: "percent becomes a literal", input: "100%", want: `100\%`},
		{name: "underscore becomes a literal", input: "a_c", want: `a\_c`},
		{
			// The backslash must be doubled first, or the escapes added here would
			// themselves be escaped and the wildcards would survive.
			name:  "backslash is doubled before the wildcards",
			input: `a\%b`,
			want:  `a\\\%b`,
		},
		{name: "empty stays empty", input: "", want: ""},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := EscapeLike(testCase.input); got != testCase.want {
				t.Errorf("EscapeLike(%q) = %q, want %q", testCase.input, got, testCase.want)
			}
		})
	}
}
