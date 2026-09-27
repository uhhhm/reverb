package download

import "testing"

func TestParseSectionTime(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want float64
	}{
		{"90", 90},
		{"1:30", 90},
		{"01:30", 90},
		{"1:02:30", 3750},
		{"0:00", 0},
		{"1:30.5", 90.5},
		{"  2:00  ", 120},
	} {
		got, err := ParseSectionTime(tc.in)
		if err != nil {
			t.Fatalf("ParseSectionTime(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("ParseSectionTime(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseSectionTimeRejectsGarbage(t *testing.T) {
	for _, in := range []string{"", "abc", "1:2:3:4", "1:70", "-5", "1m30s"} {
		if _, err := ParseSectionTime(in); err == nil {
			t.Errorf("ParseSectionTime(%q) = nil error, want failure", in)
		}
	}
}
