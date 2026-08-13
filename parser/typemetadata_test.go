package parser_test

import (
	"os"
	"testing"

	"github.com/siyka-au/twincat-analytics-go/parser"
)

// TestParseTypeMetadata_StructTypeTest exercises the record-parsing/reslicing
// logic in typemetadata.go against a real capture, isolated from sample
// decoding. This is the right place to catch a reslicing regression (see the
// warning on parseTypeRecord) since a wrong-offset bug here typically still
// "succeeds" with wrong member names/offsets rather than erroring.
func TestParseTypeMetadata_StructTypeTest(t *testing.T) {
	payload, err := os.ReadFile("../testdata/fixtures/StructTypeTest/captures/capture-20260623T0456/symbols/message-20260623T045600.bin")
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	stream, err := parser.ParseSymbolStream(payload)
	if err != nil {
		t.Fatalf("ParseSymbolStream: %v", err)
	}
	if len(stream.TypeMetadata) == 0 {
		t.Fatal("expected non-empty TypeMetadata")
	}

	defs, err := parser.ParseTypeMetadata(stream.TypeMetadata)
	if err != nil {
		t.Fatalf("ParseTypeMetadata: %v", err)
	}

	nested, ok := defs["NestedData"]
	if !ok {
		t.Fatal("NestedData definition not found")
	}
	if len(nested.Members) != 2 {
		t.Fatalf("NestedData: got %d members, want 2", len(nested.Members))
	}
	wantNested := map[string]string{"counter": "DINT", "active": "BOOL"}
	for _, m := range nested.Members {
		if want, ok := wantNested[m.Name]; !ok || m.TypeName != want {
			t.Errorf("NestedData member %q: type=%q (unexpected)", m.Name, m.TypeName)
		}
	}

	reading, ok := defs["SensorReading"]
	if !ok {
		t.Fatal("SensorReading definition not found")
	}
	if len(reading.Members) != 6 {
		t.Fatalf("SensorReading: got %d members, want 6", len(reading.Members))
	}
	last := reading.Members[len(reading.Members)-1]
	if last.Name != "inner" || last.TypeName != "NestedData" {
		t.Errorf("SensorReading last member: got %s:%s, want inner:NestedData", last.Name, last.TypeName)
	}

	someEnum, ok := defs["SomeEnum"]
	if !ok {
		t.Fatal("SomeEnum definition not found")
	}
	if !hasEnumValue(someEnum.EnumValues, "Fluffy", 1) {
		t.Errorf("SomeEnum: expected Fluffy=1 among %+v", someEnum.EnumValues)
	}

	anonEnum, ok := defs["Implicit_Enum__Main__anon_enum"]
	if !ok {
		t.Fatal("Implicit_Enum__Main__anon_enum definition not found")
	}
	if !hasEnumValue(anonEnum.EnumValues, "Dongers", 1) {
		t.Errorf("Implicit_Enum__Main__anon_enum: expected Dongers=1 among %+v", anonEnum.EnumValues)
	}
}

func hasEnumValue(values []parser.EnumValue, name string, numeric int64) bool {
	for _, v := range values {
		if v.Name == name && v.NumericValue == numeric {
			return true
		}
	}
	return false
}
