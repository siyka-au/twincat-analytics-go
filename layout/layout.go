package layout

import (
	"bytes"
	"encoding/binary"
	"math"
	"regexp"
	"sort"
	"strconv"
	"time"
	"unicode/utf16"

	"github.com/siyka-au/twincat-analytics-go/parser"
)

// Field describes a single symbol within a data sample.
type Field struct {
	// Name is the fully-qualified ADS symbol name, e.g. "Main.var_bool".
	Name string

	// TypeName is the IEC/TwinCAT type string, e.g. "BOOL", "DINT", "STRING(80)".
	TypeName string

	// IndexOffset is the raw ADS index offset from the symbol stream.
	IndexOffset uint32

	// RelativeOffset is the byte offset within a data sample payload
	// (= IndexOffset − Layout.BaseOffset).
	RelativeOffset uint32

	// Size is the byte count for this field within a sample.
	Size uint32

	// DataType is the ADS data-type enum value used for decoding.
	DataType parser.AdsDataType

	// IsBitType is true when the symbol_flags is_bit_value bit was set in the
	// symbol stream. Parsed and propagated from SymbolBody, but NOT currently
	// consumed by ParseSample/decodeValue: decodeValue reads the whole byte at
	// RelativeOffset and tests it non-zero, the same as any other Bit field.
	// TODO-016 tracks implementing real bit-level extraction — see there for
	// why this needs a hardware capture before it can be done safely.
	IsBitType bool
}

// FieldValue pairs a Field descriptor with the Go value decoded from a sample.
type FieldValue struct {
	Field *Field

	// Value is the decoded Go value. The concrete type depends on DataType
	// and, for ambiguous DataType values, on the field TypeName:
	//   Bit               → bool
	//   Int8              → int8,    Uint8   → uint8
	//   Int16             → int16,   Uint16  → uint16
	//   Int32             → int32
	//   Int64             → int64
	//   Real32            → float32, Real64  → float64
	//   String            → string (null-terminated UTF-8)
	//   WString           → string (UCS-2 LE, null-terminated)
	//   Uint32 (DINT)     → uint32
	//   Uint32 (TIME/TOD) → time.Duration (milliseconds)
	//   Uint32 (DATE)     → time.Time UTC (seconds since 1970-01-01, truncated to midnight)
	//   Uint32 (DT)       → time.Time UTC (seconds since 1970-01-01)
	//   Int64  (LTIME)    → time.Duration (nanoseconds)
	//   All other types   → []byte (raw copy)
	Value any
}

// Layout describes the full binary layout of a TwinCAT Analytics data sample.
// Each Layout is uniquely identified by its GUID, which matches the Hash field
// in the corresponding Bin/Tx/Symbols stream header.
type Layout struct {
	// GUID identifies this layout and matches the layout field in Bin/Tx/Data
	// message headers.
	GUID GUID

	// Fields lists all symbols present in each sample, sorted ascending by
	// RelativeOffset.
	Fields []Field

	// BaseOffset is the minimum IndexOffset across all symbols.  Subtracting
	// it from any symbol's IndexOffset gives the byte position in a sample.
	BaseOffset uint32

	// SampleDataSize is the total byte count expected per sample data payload
	// (highest RelativeOffset + Size of the last field).
	SampleDataSize uint32

	// TypeMetadata is the raw trailing type-definition bytes preserved from
	// the source SymbolStream. Empty for layouts built without a stream (e.g.
	// synthetic layouts in tests). Callers must not mutate this slice.
	TypeMetadata []byte

	// typeDefinitions holds every top-level struct-shaped type-metadata
	// record parsed from TypeMetadata, keyed by name. enumDefinitions is the
	// subset that's enum-shaped instead. Both are nil (not just empty) when
	// TypeMetadata is empty or fails to parse — decodeValue/decodeMemberValue
	// degrade to primitive-only decoding in that case, same as before this
	// type-expansion feature existed.
	typeDefinitions map[string]parser.TypeDefinition
	enumDefinitions map[string]parser.TypeDefinition
}

// HasTypeMetadata reports whether this layout carries a non-empty type table.
func (l *Layout) HasTypeMetadata() bool {
	return len(l.TypeMetadata) > 0
}

// TypeDefinitions returns the struct-shaped type-metadata records parsed from
// TypeMetadata, keyed by name. Nil when TypeMetadata is empty or failed to
// parse. Exported for callers outside this package (e.g. schema generators)
// that need to make the same struct-vs-primitive determination decodeValue
// does before any decoding happens.
func (l *Layout) TypeDefinitions() map[string]parser.TypeDefinition {
	return l.typeDefinitions
}

// EnumDefinitions returns the enum-shaped type-metadata records parsed from
// TypeMetadata, keyed by name. Nil when TypeMetadata is empty or failed to
// parse. Exported for callers outside this package (e.g. schema generators)
// that need to make the same enum-vs-primitive determination decodeValue
// does before any decoding happens.
func (l *Layout) EnumDefinitions() map[string]parser.TypeDefinition {
	return l.enumDefinitions
}

// NewLayoutFromStream constructs a Layout from a fully-parsed SymbolStream.
// The layout GUID is taken from the stream header's GUID field.
func NewLayoutFromStream(stream *parser.SymbolStream) *Layout {
	guid := GUID(stream.Header.GUID)

	syms := stream.Symbols
	if len(syms) == 0 {
		return &Layout{GUID: guid, TypeMetadata: stream.TypeMetadata}
	}

	// Determine the base offset (minimum IndexOffset across all symbols).
	base := syms[0].IndexOffset
	for _, sym := range syms[1:] {
		if sym.IndexOffset < base {
			base = sym.IndexOffset
		}
	}

	fields := make([]Field, 0, len(syms))
	for _, sym := range syms {
		fields = append(fields, Field{
			Name:           sym.Name,
			TypeName:       sym.TypeName,
			IndexOffset:    sym.IndexOffset,
			RelativeOffset: sym.IndexOffset - base,
			Size:           sym.Size,
			DataType:       sym.DataType,
			IsBitType:      sym.IsBitType,
		})
	}

	// Sort by relative offset so ParseSample can iterate in memory order.
	sort.Slice(fields, func(i, j int) bool {
		return fields[i].RelativeOffset < fields[j].RelativeOffset
	})

	// Total sample size = end byte of the last (highest-offset) field.
	var totalSize uint32
	for _, f := range fields {
		if end := f.RelativeOffset + f.Size; end > totalSize {
			totalSize = end
		}
	}

	typeDefs, enumDefs := buildTypeMaps(stream.TypeMetadata)

	return &Layout{
		GUID:            guid,
		Fields:          fields,
		BaseOffset:      base,
		SampleDataSize:  totalSize,
		TypeMetadata:    stream.TypeMetadata,
		typeDefinitions: typeDefs,
		enumDefinitions: enumDefs,
	}
}

// buildTypeMaps parses metadata into struct-shaped and enum-shaped
// TypeDefinition maps, degrading to nil maps (decode falls back to
// primitives) on any parse error or panic. Mirrors Layout's constructor in
// tcanalytics4j, which wraps the equivalent call in a try/catch swallowing
// RuntimeException. The extra recover() here is defense-in-depth beyond
// typemetadata.go's explicit bounds checks: cmd/service and cmd/tcaly-mqtt
// are long-running processes decoding live-captured, not-fully-trusted
// streams, so an unhandled panic from a missed bounds check would be a worse
// failure mode here than in the JVM, which turns that class of bug into a
// catchable exception for free.
func buildTypeMaps(metadata []byte) (typeDefs, enumDefs map[string]parser.TypeDefinition) {
	if len(metadata) == 0 {
		return nil, nil
	}
	defer func() {
		if r := recover(); r != nil {
			typeDefs, enumDefs = nil, nil
		}
	}()
	defs, err := parser.ParseTypeMetadata(metadata)
	if err != nil {
		return nil, nil
	}
	enums := make(map[string]parser.TypeDefinition)
	for name, def := range defs {
		if len(def.EnumValues) > 0 {
			enums[name] = def
		}
	}
	return defs, enums
}

// ParseSample decodes a raw sample data blob using this layout and returns one
// FieldValue per symbol.  Fields whose byte range falls outside data are
// silently skipped.  data should be at least SampleDataSize bytes long.
func (l *Layout) ParseSample(data []byte) []FieldValue {
	results := make([]FieldValue, 0, len(l.Fields))
	for i := range l.Fields {
		f := &l.Fields[i]
		end := f.RelativeOffset + f.Size
		if int(end) > len(data) {
			continue
		}
		results = append(results, FieldValue{
			Field: f,
			Value: l.decodeValue(data[f.RelativeOffset:end], f),
		})
	}
	return results
}

// arrayTypeNamePattern matches IEC array type names as reported in the
// symbol stream, e.g. "ARRAY [0..15] OF INT" or "ARRAY [0..7] OF STRING(10)".
var arrayTypeNamePattern = regexp.MustCompile(`^ARRAY\s*\[(-?\d+)\.\.(-?\d+)\]\s*OF\s+(.+)$`)

// ParseArrayTypeName reports whether typeName is an IEC array type name,
// returning the element count and the element's own type name (e.g. "INT",
// "STRING(10)"). Exported so callers outside this package (e.g. the Parquet
// writer, which needs to pick a schema node before any decoding happens) can
// make the same "is this field an array" determination decodeValue does.
func ParseArrayTypeName(typeName string) (count int, elementTypeName string, ok bool) {
	m := arrayTypeNamePattern.FindStringSubmatch(typeName)
	if m == nil {
		return 0, "", false
	}
	lo, errLo := strconv.Atoi(m[1])
	hi, errHi := strconv.Atoi(m[2])
	if errLo != nil || errHi != nil || hi < lo {
		return 0, "", false
	}
	return hi - lo + 1, m[3], true
}

// decodeValue expands array, struct, and enum-typed fields via TypeName and
// the layout's parsed type metadata before falling back to primitive
// decoding. Array handling has no Java equivalent to port — tcanalytics4j
// doesn't implement it either — added here because the symbol stream reports
// an array field's DataType as its *element* type with Size = elementSize *
// count, and decodePrimitiveValue's scalar cases were silently reading only
// the first element and discarding the rest with no error.
func (l *Layout) decodeValue(raw []byte, f *Field) any {
	if count, elemTypeName, ok := ParseArrayTypeName(f.TypeName); ok {
		return l.decodeArray(raw, count, elemTypeName, f.DataType)
	}
	if def, ok := l.typeDefinitions[f.TypeName]; ok && len(def.Members) > 0 {
		return l.decodeStruct(raw, def)
	}
	if def, ok := l.enumDefinitions[f.TypeName]; ok {
		return l.decodeEnum(raw, def)
	}
	return decodePrimitiveValue(raw, f.DataType, f.TypeName)
}

// decodeArray decodes an IEC array field into a []any of count decoded
// elements. The wire format lays array elements out as a flat fixed-stride
// block, so each element is len(raw)/count bytes wide. Falls back to a raw
// copy if the stride doesn't divide evenly — safer than guessing at a
// misparsed count.
func (l *Layout) decodeArray(raw []byte, count int, elemTypeName string, elemDataType parser.AdsDataType) any {
	if count <= 0 || len(raw)%count != 0 {
		out := make([]byte, len(raw))
		copy(out, raw)
		return out
	}
	elemSize := len(raw) / count
	values := make([]any, count)
	for i := 0; i < count; i++ {
		elemRaw := raw[i*elemSize : (i+1)*elemSize]
		values[i] = l.decodeElementValue(elemRaw, elemTypeName, elemDataType)
	}
	return values
}

// decodeElementValue decodes one array element, expanding struct/enum
// element types the same way decodeValue does for top-level fields. Array-
// of-array and array-typed struct members aren't handled — no fixture
// exercises either, and the Java reference doesn't implement them.
func (l *Layout) decodeElementValue(raw []byte, typeName string, dataType parser.AdsDataType) any {
	if def, ok := l.typeDefinitions[typeName]; ok && len(def.Members) > 0 {
		return l.decodeStruct(raw, def)
	}
	if def, ok := l.enumDefinitions[typeName]; ok {
		return l.decodeEnum(raw, def)
	}
	return decodePrimitiveValue(raw, dataType, typeName)
}

// decodeMemberValue is decodeValue's counterpart for a struct member — same
// lookup-then-recurse logic, keyed on the member's own TypeName. This is what
// makes struct-of-struct nesting work: a member whose type is itself a
// struct recurses back into decodeStruct. Unlike decodeValue, this doesn't
// check ParseArrayTypeName — an array-typed struct member would decode wrong
// the same way top-level array fields used to, but no fixture has one, so
// it's left as a known gap rather than added speculatively.
func (l *Layout) decodeMemberValue(raw []byte, m *parser.TypeMember) any {
	if def, ok := l.typeDefinitions[m.TypeName]; ok && len(def.Members) > 0 {
		return l.decodeStruct(raw, def)
	}
	if def, ok := l.enumDefinitions[m.TypeName]; ok {
		return l.decodeEnum(raw, def)
	}
	return decodePrimitiveValue(raw, m.DataType, m.TypeName)
}

// decodeStruct decodes each member of a struct-shaped TypeDefinition from its
// byte range within raw, recursing via decodeMemberValue. No depth limit or
// cycle guard — matches the Java reference; a self-referential type table
// isn't a regression introduced by this port, so not solved here.
func (l *Layout) decodeStruct(raw []byte, def parser.TypeDefinition) map[string]any {
	values := make(map[string]any, len(def.Members))
	for i := range def.Members {
		m := &def.Members[i]
		end := m.ByteOffset + m.SizeBytes
		if m.ByteOffset < 0 || end > len(raw) {
			continue
		}
		values[m.Name] = l.decodeMemberValue(raw[m.ByteOffset:end], m)
	}
	return values
}

// decodeEnum decodes the base primitive value and looks up its matching
// EnumValue by numeric value. The "name" key is omitted when no enum value
// matches the decoded number (e.g. an out-of-range or corrupt sample).
func (l *Layout) decodeEnum(raw []byte, def parser.TypeDefinition) any {
	baseType := inferEnumBaseType(def.BaseTypeName, def.SizeBytes)
	primitive := decodePrimitiveValue(raw, baseType, def.BaseTypeName)
	numeric, ok := asInt64(primitive)
	if !ok {
		return primitive
	}
	result := map[string]any{"value": numeric, "type": def.Name}
	for _, ev := range def.EnumValues {
		if ev.NumericValue == numeric {
			result["name"] = ev.Name
			break
		}
	}
	return result
}

// asInt64 extracts an int64 from any of decodePrimitiveValue's integer
// return types, mirroring Java's decodeEnum "instanceof Number" guard.
func asInt64(v any) (int64, bool) {
	switch x := v.(type) {
	case int8:
		return int64(x), true
	case uint8:
		return int64(x), true
	case int16:
		return int64(x), true
	case uint16:
		return int64(x), true
	case int32:
		return int64(x), true
	case uint32:
		return int64(x), true
	case int64:
		return x, true
	case uint64:
		return int64(x), true
	default:
		return 0, false
	}
}

// inferEnumBaseType maps an enum's declared base type name to the
// AdsDataType decodePrimitiveValue needs to decode its underlying storage.
func inferEnumBaseType(baseTypeName string, sizeBytes int) parser.AdsDataType {
	switch baseTypeName {
	case "BOOL":
		return parser.AdsDataTypeBit
	case "SINT":
		return parser.AdsDataTypeInt8
	case "USINT", "BYTE":
		return parser.AdsDataTypeUint8
	case "INT":
		return parser.AdsDataTypeInt16
	case "UINT", "WORD":
		return parser.AdsDataTypeUint16
	case "DINT":
		return parser.AdsDataTypeInt32
	case "UDINT", "DWORD":
		return parser.AdsDataTypeUint32
	case "LINT":
		return parser.AdsDataTypeInt64
	case "ULINT", "LWORD":
		return parser.AdsDataTypeUint64
	default:
		switch {
		case sizeBytes <= 1:
			return parser.AdsDataTypeUint8
		case sizeBytes <= 2:
			return parser.AdsDataTypeUint16
		default:
			return parser.AdsDataTypeUint32
		}
	}
}

// decodePrimitiveValue interprets raw bytes as the Go type corresponding to
// dataType and typeName. Falls back to a raw []byte copy for unrecognised or
// complex types (including BigType fields whose TypeName doesn't resolve to
// a parsed struct or enum definition).
func decodePrimitiveValue(raw []byte, dataType parser.AdsDataType, typeName string) any {
	switch dataType {
	case parser.AdsDataTypeBit:
		if len(raw) >= 1 {
			return raw[0] != 0
		}

	case parser.AdsDataTypeInt8:
		if len(raw) >= 1 {
			return int8(raw[0])
		}

	case parser.AdsDataTypeUint8:
		if len(raw) >= 1 {
			return raw[0]
		}

	case parser.AdsDataTypeInt16:
		if len(raw) >= 2 {
			return int16(binary.LittleEndian.Uint16(raw))
		}

	case parser.AdsDataTypeUint16:
		if len(raw) >= 2 {
			return binary.LittleEndian.Uint16(raw)
		}

	case parser.AdsDataTypeInt32:
		if len(raw) >= 4 {
			return int32(binary.LittleEndian.Uint32(raw))
		}

	case parser.AdsDataTypeUint32:
		if len(raw) >= 4 {
			v := binary.LittleEndian.Uint32(raw)
			switch typeName {
			case "TIME", "TIME_OF_DAY", "TOD":
				// Milliseconds → time.Duration.
				return time.Duration(v) * time.Millisecond
			case "DATE":
				// Seconds since 1970-01-01 UTC (TwinCAT stores DATE as
				// uint32 Unix seconds, truncated to midnight).
				return time.Unix(int64(v), 0).UTC()
			case "DATE_AND_TIME", "DT":
				// Seconds since 1970-01-01 UTC.
				return time.Unix(int64(v), 0).UTC()
			default:
				return v
			}
		}

	case parser.AdsDataTypeInt64:
		if len(raw) >= 8 {
			v := int64(binary.LittleEndian.Uint64(raw))
			if typeName == "LTIME" {
				// Nanoseconds → time.Duration.
				return time.Duration(v)
			}
			return v
		}

	case parser.AdsDataTypeUint64:
		if len(raw) >= 8 {
			return binary.LittleEndian.Uint64(raw)
		}

	case parser.AdsDataTypeReal32:
		if len(raw) >= 4 {
			return math.Float32frombits(binary.LittleEndian.Uint32(raw))
		}

	case parser.AdsDataTypeReal64:
		if len(raw) >= 8 {
			return math.Float64frombits(binary.LittleEndian.Uint64(raw))
		}

	case parser.AdsDataTypeString:
		// Null-terminated UTF-8; trim at the first NUL byte.
		s := raw
		if i := bytes.IndexByte(s, 0); i >= 0 {
			s = s[:i]
		}
		return string(s)

	case parser.AdsDataTypeWString:
		// UCS-2 LE pairs; stop at the null terminator (0x0000).
		n := len(raw) / 2
		u16 := make([]uint16, 0, n)
		for i := 0; i+1 < len(raw); i += 2 {
			cp := binary.LittleEndian.Uint16(raw[i : i+2])
			if cp == 0 {
				break
			}
			u16 = append(u16, cp)
		}
		return string(utf16.Decode(u16))
	}

	// Fallback: return a defensive copy of the raw bytes for complex /
	// unrecognised types (BigType, struct, array, etc.).
	out := make([]byte, len(raw))
	copy(out, raw)
	return out
}
