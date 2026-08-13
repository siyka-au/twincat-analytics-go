// Package parquet provides a streaming Parquet writer for TwinCAT Analytics
// data decoded via internal/layout.
//
// Schema is derived dynamically at open time from the stream's []layout.Field.
// A timestamp column is prepended as the first column (by name, so it falls
// into the alphabetically-sorted schema position automatically).
package parquet

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	parquetgo "github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress"

	"github.com/siyka-au/twincat-analytics-go/layout"
	"github.com/siyka-au/twincat-analytics-go/parser"
)

// Writer streams decoded TwinCAT Analytics samples to a Parquet file.
type Writer struct {
	w       *parquetgo.Writer
	builder *parquetgo.RowBuilder
	fields  []layout.Field

	// colIdx maps sanitized column name -> index in the schema (alphabetic order)
	colIdx map[string]int
}

// CompressionFromString maps a codec name to the corresponding compress.Codec.
// Accepted values (case-insensitive): uncompressed, snappy, gzip, zstd, lz4raw, brotli.
func CompressionFromString(s string) (compress.Codec, error) {
	switch strings.ToLower(s) {
	case "uncompressed":
		return &parquetgo.Uncompressed, nil
	case "snappy":
		return &parquetgo.Snappy, nil
	case "gzip":
		return &parquetgo.Gzip, nil
	case "zstd":
		return &parquetgo.Zstd, nil
	case "lz4raw":
		return &parquetgo.Lz4Raw, nil
	case "brotli":
		return &parquetgo.Brotli, nil
	default:
		return nil, fmt.Errorf("unknown compression codec %q: must be one of uncompressed, snappy, gzip, zstd, lz4raw, brotli", s)
	}
}

// New creates a Writer that writes Parquet rows to out.
// The schema is derived from fields; call Close when done to flush metadata.
// compression sets the codec for all columns; pass nil for uncompressed output.
func New(out io.Writer, fields []layout.Field, compression compress.Codec) (*Writer, error) {
	group := parquetgo.Group{}
	group["timestamp"] = parquetgo.Timestamp(parquetgo.Nanosecond)

	for _, f := range fields {
		node, err := nodeForField(f)
		if err != nil {
			return nil, fmt.Errorf("parquet: build schema for %q: %w", f.Name, err)
		}
		group[sanitizeName(f.Name)] = node
	}

	schema := parquetgo.NewSchema("twincat_analytics", group)

	// Build column-index map from the schema's sorted field list.
	colIdx := make(map[string]int, len(group))
	for i, sf := range schema.Fields() {
		colIdx[sf.Name()] = i
	}

	pw := parquetgo.NewWriter(out, &parquetgo.WriterConfig{Schema: schema, Compression: compression})

	return &Writer{
		w:       pw,
		builder: parquetgo.NewRowBuilder(schema),
		fields:  fields,
		colIdx:  colIdx,
	}, nil
}

// WriteRow writes one decoded sample to the Parquet file.
// ts is the sample timestamp; values must be in the same order as the fields
// slice that was passed to New.
func (w *Writer) WriteRow(ts time.Time, values []layout.FieldValue) error {
	w.builder.Reset()

	// Write the timestamp column.
	if idx, ok := w.colIdx["timestamp"]; ok {
		w.builder.Add(idx, parquetgo.Int64Value(ts.UnixNano()))
	}

	for i, fv := range values {
		if i >= len(w.fields) {
			break
		}
		name := sanitizeName(fv.Field.Name)
		idx, ok := w.colIdx[name]
		if !ok {
			continue
		}
		v, err := toParquetValue(fv)
		if err != nil {
			return fmt.Errorf("parquet: column %q: %w", fv.Field.Name, err)
		}
		w.builder.Add(idx, v)
	}

	row := w.builder.Row()
	_, err := w.w.WriteRows([]parquetgo.Row{row})
	return err
}

// Close flushes all buffered data and writes the Parquet file footer.
func (w *Writer) Close() error { return w.w.Close() }

// nodeForField maps a layout.Field to the appropriate parquet-go Node.
func nodeForField(f layout.Field) (parquetgo.Node, error) {
	if _, _, ok := layout.ParseArrayTypeName(f.TypeName); ok {
		// Array fields decode to []any (layout.decodeArray), not a scalar of
		// f.DataType (which reports the *element* type) -- route to the same
		// JSON-in-byte-array column as struct/enum fields rather than let the
		// DataType switch below pick a scalar node that toParquetValue can't
		// write a []any into.
		return parquetgo.Leaf(parquetgo.ByteArrayType), nil
	}

	switch f.DataType {
	case parser.AdsDataTypeBit:
		return parquetgo.Leaf(parquetgo.BooleanType), nil

	case parser.AdsDataTypeInt8,
		parser.AdsDataTypeUint8,
		parser.AdsDataTypeInt16,
		parser.AdsDataTypeUint16,
		parser.AdsDataTypeInt32:
		return parquetgo.Leaf(parquetgo.Int32Type), nil

	case parser.AdsDataTypeUint32,
		parser.AdsDataTypeInt64,
		parser.AdsDataTypeUint64:
		return parquetgo.Leaf(parquetgo.Int64Type), nil

	case parser.AdsDataTypeReal32:
		return parquetgo.Leaf(parquetgo.FloatType), nil

	case parser.AdsDataTypeReal64:
		return parquetgo.Leaf(parquetgo.DoubleType), nil

	case parser.AdsDataTypeString, parser.AdsDataTypeWString:
		return parquetgo.String(), nil

	default:
		// TIME/TOD/DT (decoded as time.Duration or time.Time),
		// LTIME, DATE, BigType ([]byte), unknown -> raw bytes.
		return parquetgo.Leaf(parquetgo.ByteArrayType), nil
	}
}

// toParquetValue converts a layout.FieldValue to a parquet-go Value.
func toParquetValue(fv layout.FieldValue) (parquetgo.Value, error) {
	switch v := fv.Value.(type) {
	case bool:
		return parquetgo.BooleanValue(v), nil
	case int8:
		return parquetgo.Int32Value(int32(v)), nil
	case uint8:
		return parquetgo.Int32Value(int32(v)), nil
	case int16:
		return parquetgo.Int32Value(int32(v)), nil
	case uint16:
		return parquetgo.Int32Value(int32(v)), nil
	case int32:
		return parquetgo.Int32Value(v), nil
	case uint32:
		return parquetgo.Int64Value(int64(v)), nil
	case int64:
		return parquetgo.Int64Value(v), nil
	case uint64:
		return parquetgo.Int64Value(int64(v)), nil
	case float32:
		return parquetgo.FloatValue(v), nil
	case float64:
		return parquetgo.DoubleValue(v), nil
	case string:
		return parquetgo.ByteArrayValue([]byte(v)), nil
	case time.Duration:
		return parquetgo.ByteArrayValue([]byte(v.String())), nil
	case time.Time:
		return parquetgo.ByteArrayValue([]byte(v.UTC().Format(time.RFC3339Nano))), nil
	case []byte:
		return parquetgo.ByteArrayValue(v), nil
	case map[string]any:
		// Struct- and enum-typed fields (BigType expansion via the layout's
		// type metadata) decode to a nested map with no fixed shape, so they
		// can't map to a scalar Parquet column the way primitives do. JSON-
		// encode into the same ByteArrayType column nodeForField already
		// assigns BigType fields by default. This is a pragmatic stopgap,
		// not real nested-column support — that would need dynamic per-field
		// schema generation and is a separate piece of work.
		b, err := json.Marshal(v)
		if err != nil {
			return parquetgo.Value{}, fmt.Errorf("marshal struct/enum field: %w", err)
		}
		return parquetgo.ByteArrayValue(b), nil
	case []any:
		// Array fields (layout.decodeArray) — same JSON-in-byte-array
		// stopgap as the map[string]any case above, and for the same
		// reason: nodeForField already routes array fields to ByteArrayType.
		b, err := json.Marshal(v)
		if err != nil {
			return parquetgo.Value{}, fmt.Errorf("marshal array field: %w", err)
		}
		return parquetgo.ByteArrayValue(b), nil
	case nil:
		return parquetgo.NullValue(), nil
	default:
		return parquetgo.Value{}, fmt.Errorf("unsupported value type %T", fv.Value)
	}
}

// sanitizeName replaces dots in TwinCAT symbol names with "__" so that the
// full qualified path is preserved unambiguously as a Parquet column name.
func sanitizeName(name string) string {
	out := make([]byte, 0, len(name)+8)
	for i := 0; i < len(name); i++ {
		if name[i] == '.' {
			out = append(out, '_', '_')
		} else {
			out = append(out, name[i])
		}
	}
	return string(out)
}
