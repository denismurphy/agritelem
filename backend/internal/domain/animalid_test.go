package domain

import (
	"errors"
	"testing"
)

func TestParseAnimalID(t *testing.T) {
	tests := []struct {
		name     string
		scheme   string
		id       string
		wantCode Code // empty means valid
		wantKind CodeKind
	}{
		{"ISO 11785 Irish country code", SchemeISO11785, "372000012345678", "", CodeKindCountry},
		{"ISO 11785 manufacturer code", SchemeISO11785, "900123456789012", "", CodeKindManufacturer},
		{"ISO 11785 upper manufacturer bound", SchemeISO11785, "998123456789012", "", CodeKindManufacturer},
		{"ISO 11785 test code", SchemeISO11785, "999000000000001", "", CodeKindTest},
		{"EU German country code", SchemeEUAnimalID, "276000312312345", "", CodeKindCountry},
		{"EU rejects manufacturer code", SchemeEUAnimalID, "900123456789012", CodeInvalidAnimalID, 0},
		{"EU rejects test code", SchemeEUAnimalID, "999000000000001", CodeInvalidAnimalID, 0},
		{"unassigned country code", SchemeISO11785, "001000012345678", CodeInvalidAnimalID, 0},
		{"zero country code", SchemeISO11785, "000000012345678", CodeInvalidAnimalID, 0},
		{"14 digits", SchemeISO11785, "37200001234567", CodeInvalidAnimalID, 0},
		{"16 digits", SchemeISO11785, "3720000123456789", CodeInvalidAnimalID, 0},
		{"letters", SchemeISO11785, "37200001234567A", CodeInvalidAnimalID, 0},
		{"fullwidth unicode digits", SchemeISO11785, "３７２000012345678", CodeInvalidAnimalID, 0},
		{"surrounding space", SchemeISO11785, " 372000012345678", CodeInvalidAnimalID, 0},
		{"empty id", SchemeISO11785, "", CodeInvalidAnimalID, 0},
		{"unsupported scheme", "au.nlis", "QABC1234XBC2345", CodeUnsupportedAnimalID, 0},
		{"empty scheme", "", "372000012345678", CodeUnsupportedAnimalID, 0},
		{"scheme is case sensitive", "EU.ANIMALID", "276000312312345", CodeUnsupportedAnimalID, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseAnimalID(tt.scheme, tt.id)
			if tt.wantCode != "" {
				var p *Problem
				if !errors.As(err, &p) || p.Code != tt.wantCode {
					t.Fatalf("err = %v, want Problem with code %s", err, tt.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != (AnimalID{Scheme: tt.scheme, ID: tt.id}) {
				t.Errorf("got %+v", got)
			}
			if got.Kind() != tt.wantKind {
				t.Errorf("Kind() = %v, want %v", got.Kind(), tt.wantKind)
			}
		})
	}
}

func FuzzParseAnimalID(f *testing.F) {
	for _, seed := range []string{"372000012345678", "900123456789012", "999000000000001", "", "３７２", "0000000000000000"} {
		f.Add(SchemeISO11785, seed)
		f.Add(SchemeEUAnimalID, seed)
	}
	f.Fuzz(func(t *testing.T, scheme, id string) {
		got, err := ParseAnimalID(scheme, id)
		if err != nil {
			var p *Problem
			if !errors.As(err, &p) {
				t.Fatalf("error %v is not a *Problem", err)
			}
			return
		}
		if len(id) != 15 {
			t.Fatalf("accepted %q of length %d", id, len(id))
		}
		for _, r := range id {
			if r < '0' || r > '9' {
				t.Fatalf("accepted non-ASCII-digit %q in %q", r, id)
			}
		}
		if got.Kind() == 0 {
			t.Fatalf("accepted %q without a code kind", id)
		}
		if scheme == SchemeEUAnimalID && got.Kind() != CodeKindCountry {
			t.Fatalf("eu.animalId accepted non-country code in %q", id)
		}
	})
}
