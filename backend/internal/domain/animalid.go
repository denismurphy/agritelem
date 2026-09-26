package domain

// Animal identifier schemes from ICAR ADE well-known/icarAnimalIdentifierType.md.
const (
	// SchemeISO11785 is an ISO 11785 RFID code number in decimal: 3 digits of
	// country or manufacturer code followed by a 12-digit national ID.
	SchemeISO11785 = "std.iso.11785"
	// SchemeEUAnimalID is the EU form of ISO 11784 whose first 3 digits are an
	// ISO 3166-1 numeric country code.
	SchemeEUAnimalID = "eu.animalId"
)

const iso11784Digits = 15

// CodeKind says what the leading 3 digits of an ISO 11784 number denote.
type CodeKind int

// Kinds of ISO 11784 leading code.
const (
	CodeKindCountry CodeKind = iota + 1
	CodeKindManufacturer
	CodeKindTest
)

// AnimalID is a validated ICAR animal identifier. The zero value is invalid;
// construct one with ParseAnimalID.
type AnimalID struct {
	Scheme string
	ID     string
}

// ParseAnimalID validates id under scheme. Only the ISO 11784/11785 schemes
// are supported; any other scheme is rejected rather than stored unchecked.
func ParseAnimalID(scheme, id string) (AnimalID, error) {
	if scheme != SchemeISO11785 && scheme != SchemeEUAnimalID {
		return AnimalID{}, problemf(CodeUnsupportedAnimalID, "/scheme",
			"animal identifier scheme %q is not supported; use %q or %q", scheme, SchemeISO11785, SchemeEUAnimalID)
	}
	if len(id) != iso11784Digits || !allASCIIDigits(id) {
		return AnimalID{}, problemf(CodeInvalidAnimalID, "/id",
			"%s identifiers are %d ASCII digits, got %q", scheme, iso11784Digits, id)
	}
	a := AnimalID{Scheme: scheme, ID: id}
	switch kind := a.Kind(); {
	case kind == 0:
		return AnimalID{}, problemf(CodeInvalidAnimalID, "/id",
			"leading code %s is neither an ISO 3166-1 numeric country code nor a manufacturer code (900-998)", id[:3])
	case scheme == SchemeEUAnimalID && kind != CodeKindCountry:
		return AnimalID{}, problemf(CodeInvalidAnimalID, "/id",
			"%s requires an ISO 3166-1 numeric country code, got %s", scheme, id[:3])
	}
	return a, nil
}

// Kind reports what the leading code of the identifier denotes, or 0 if it
// denotes nothing valid.
func (a AnimalID) Kind() CodeKind {
	if len(a.ID) < 3 || !allASCIIDigits(a.ID[:3]) {
		return 0
	}
	code := a.ID[:3]
	switch {
	case code == "999":
		return CodeKindTest
	case code >= "900":
		return CodeKindManufacturer
	case iso3166Numeric[code] != "":
		return CodeKindCountry
	default:
		return 0
	}
}

func allASCIIDigits(s string) bool {
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}
