package parquet

import (
	"encoding/json"
	"testing"

	parquetgo "github.com/parquet-go/parquet-go"

	"github.com/siyka-au/twincat-analytics-go/layout"
)

// TestToParquetValue_StructField verifies the map[string]any case added for
// BigType struct/enum expansion (see internal/layout's decodeStruct/
// decodeEnum). Before this case existed, any struct- or enum-typed field
// would make toParquetValue return an "unsupported value type" error for
// every row, since decodeValue started returning nested maps instead of raw
// []byte for such fields. This is a stopgap (JSON-in-a-byte-array-column),
// not real nested-column support — just confirms it doesn't error and round-
// trips the data.
func TestToParquetValue_StructField(t *testing.T) {
	f := &layout.Field{Name: "Main.reading", DataType: 65 /* BigType */}
	fv := layout.FieldValue{
		Field: f,
		Value: map[string]any{
			"raw":   uint32(4226),
			"inner": map[string]any{"counter": int32(13883076), "active": true},
		},
	}

	v, err := toParquetValue(fv)
	if err != nil {
		t.Fatalf("toParquetValue: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(v.ByteArray(), &got); err != nil {
		t.Fatalf("unmarshal round-trip: %v", err)
	}
	if got["raw"].(float64) != 4226 {
		t.Errorf("raw: got %v, want 4226", got["raw"])
	}
	inner, ok := got["inner"].(map[string]any)
	if !ok {
		t.Fatalf("inner: got %T, want map[string]any", got["inner"])
	}
	if inner["counter"].(float64) != 13883076 {
		t.Errorf("inner.counter: got %v, want 13883076", inner["counter"])
	}
}

// TestToParquetValue_EnumField mirrors the enum shape decodeEnum produces
// ({"value", "name", "type"}).
func TestToParquetValue_EnumField(t *testing.T) {
	f := &layout.Field{Name: "Main.some_enum", DataType: 2}
	fv := layout.FieldValue{
		Field: f,
		Value: map[string]any{"value": int64(1), "name": "Fluffy", "type": "SomeEnum"},
	}

	v, err := toParquetValue(fv)
	if err != nil {
		t.Fatalf("toParquetValue: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(v.ByteArray(), &got); err != nil {
		t.Fatalf("unmarshal round-trip: %v", err)
	}
	if got["name"] != "Fluffy" {
		t.Errorf("name: got %v, want Fluffy", got["name"])
	}
}

// TestNodeForField_ArrayType verifies array fields get routed to
// ByteArrayType rather than the scalar node their (element-typed) DataType
// would otherwise pick. Before this check existed in nodeForField, an array
// field like "ARRAY [0..15] OF INT" (DataType=Int16) got an Int32Type schema
// node, but decodeArray produces a []any at write time — WriteRow would have
// failed schema validation on the very first array-typed row.
func TestNodeForField_ArrayType(t *testing.T) {
	f := layout.Field{Name: "Main.var_array_int", TypeName: "ARRAY [0..15] OF INT", DataType: 2}
	node, err := nodeForField(f)
	if err != nil {
		t.Fatalf("nodeForField: %v", err)
	}
	if node.Type() != parquetgo.ByteArrayType {
		t.Errorf("got type %v, want ByteArrayType", node.Type())
	}
}

// TestToParquetValue_ArrayField verifies the []any case added for array
// field expansion (see internal/layout's decodeArray).
func TestToParquetValue_ArrayField(t *testing.T) {
	f := &layout.Field{Name: "Main.var_array_int", TypeName: "ARRAY [0..2] OF INT", DataType: 2}
	fv := layout.FieldValue{
		Field: f,
		Value: []any{int16(193), int16(386), int16(579)},
	}

	v, err := toParquetValue(fv)
	if err != nil {
		t.Fatalf("toParquetValue: %v", err)
	}

	var got []any
	if err := json.Unmarshal(v.ByteArray(), &got); err != nil {
		t.Fatalf("unmarshal round-trip: %v", err)
	}
	if len(got) != 3 || got[1].(float64) != 386 {
		t.Errorf("got %v, want [193 386 579]", got)
	}
}
