package geo

import "testing"

func TestLookupValid(t *testing.T) {
	loc, err := Lookup("90210")
	if err != nil {
		t.Fatalf("Lookup(90210) unexpected error: %v", err)
	}
	if loc.ZipCode != "90210" {
		t.Errorf("ZipCode = %q, want 90210", loc.ZipCode)
	}
	if loc.Lat < 34 || loc.Lat > 35 {
		t.Errorf("Lat = %f, expected ~34.x for Beverly Hills", loc.Lat)
	}
	if loc.City != "Beverly Hills" {
		t.Errorf("City = %q, want Beverly Hills", loc.City)
	}
	if loc.State != "CA" {
		t.Errorf("State = %q, want CA", loc.State)
	}
}

func TestLookupPadded(t *testing.T) {
	// Leading-zero ZIP codes (e.g. Connecticut)
	loc, err := Lookup("06511")
	if err != nil {
		t.Fatalf("Lookup(06511) unexpected error: %v", err)
	}
	if loc.ZipCode != "06511" {
		t.Errorf("ZipCode = %q, want 06511", loc.ZipCode)
	}
	if loc.City != "New Haven" {
		t.Errorf("City = %q, want New Haven", loc.City)
	}
	if loc.State != "CT" {
		t.Errorf("State = %q, want CT", loc.State)
	}
}

func TestLookupShortZIP(t *testing.T) {
	// Short input should be zero-padded. Use 6511 → 06511 (New Haven, CT).
	loc, err := Lookup("6511")
	if err != nil {
		t.Fatalf("Lookup(6511) unexpected error: %v", err)
	}
	if loc.ZipCode != "06511" {
		t.Errorf("ZipCode = %q, want 06511", loc.ZipCode)
	}
}

func TestLookupZIPPlus4(t *testing.T) {
	// ZIP+4 should be truncated to first 5 digits
	loc, err := Lookup("90210-1234")
	if err != nil {
		t.Fatalf("Lookup(90210-1234) unexpected error: %v", err)
	}
	if loc.ZipCode != "90210" {
		t.Errorf("ZipCode = %q, want 90210", loc.ZipCode)
	}
}

func TestLookupInvalid(t *testing.T) {
	_, err := Lookup("00000")
	if err == nil {
		t.Error("Lookup(00000) expected error, got nil")
	}
}

func TestLookupEmpty(t *testing.T) {
	_, err := Lookup("")
	if err == nil {
		t.Error("Lookup(\"\") expected error, got nil")
	}
}

func TestLookupWhitespace(t *testing.T) {
	loc, err := Lookup("  90210  ")
	if err != nil {
		t.Fatalf("Lookup with whitespace unexpected error: %v", err)
	}
	if loc.ZipCode != "90210" {
		t.Errorf("ZipCode = %q, want 90210", loc.ZipCode)
	}
}
