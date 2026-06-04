package symstore

import "testing"

func TestNormalizePDB(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"0C1033F78632492E91C6C314B72E1920ffffffff", "0c1033f7-8632-492e-91c6-c314b72e1920-ffffffff", true},
		{"0c1033f78632492e91c6c314b72e192000000000", "0c1033f7-8632-492e-91c6-c314b72e1920", true}, // age 0 -> no appendix
		{"0c1033f78632492e91c6c314b72e192000000001", "0c1033f7-8632-492e-91c6-c314b72e1920-1", true},
		{"tooshort", "", false},
		{"zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz00000001", "", false},
	}
	for _, tc := range cases {
		got, ok := normalizePDB(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("normalizePDB(%q) = (%q,%v), want (%q,%v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestELFDebugID(t *testing.T) {
	// First 16 bytes 01..10 little-endian-swapped into display order.
	got, ok := elfDebugID("0102030405060708090a0b0c0d0e0f10aabbccdd")
	if !ok || got != "04030201-0605-0807-090a-0b0c0d0e0f10" {
		t.Fatalf("elfDebugID = (%q,%v)", got, ok)
	}

	if _, ok := elfDebugID("0102"); ok {
		t.Fatalf("short build-id should fail")
	}
}

func TestNormalizeUUID(t *testing.T) {
	simple, dashed, ok := normalizeUUID("67E9247C-814E-392B-A027-DBDE6748FCBF")
	if !ok || simple != "67e9247c814e392ba027dbde6748fcbf" || dashed != "67e9247c-814e-392b-a027-dbde6748fcbf" {
		t.Fatalf("normalizeUUID = (%q,%q,%v)", simple, dashed, ok)
	}

	if _, _, ok := normalizeUUID("deadbeef"); ok {
		t.Fatalf("short uuid should fail")
	}
}
