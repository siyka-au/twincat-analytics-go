package parser

import (
	"encoding/binary"
	"fmt"
)

// Record kinds found in the type-metadata section trailing the flat symbol
// list in a Bin/Tx/Symbols payload (data[lenHeader+lenSymbols:]).
const (
	typeRecordKind       = 0x81
	fieldRecordKind      = 0x82
	minEnumPropertyBytes = 18
)

// TypeMember is one field of a struct-shaped TypeDefinition.
type TypeMember struct {
	Name       string
	TypeName   string
	DataType   AdsDataType
	SizeBytes  int
	ByteOffset int
}

// EnumValue is one named value of an enum-shaped TypeDefinition.
type EnumValue struct {
	Name         string
	NumericValue int64
}

// TypeDefinition is a parsed top-level (TYPE_RECORD_KIND) type-metadata
// record. Struct-shaped definitions have non-empty Members; enum-shaped
// (type-aliasing) definitions have non-empty EnumValues.
type TypeDefinition struct {
	Name         string
	DataType     AdsDataType
	SizeBytes    int
	BaseTypeName string
	Members      []TypeMember
	EnumValues   []EnumValue
}

// parsedRecord is the internal working shape for one decoded record (type or
// field) before it's collapsed into a public TypeDefinition.
type parsedRecord struct {
	endOffset       int
	recordKind      int
	dataType        AdsDataType
	sizeBytes       int
	offsetOrUnknown int
	name            string
	typeName        string
	children        []parsedRecord
	enumValues      []EnumValue
}

// ParseTypeMetadata parses the trailing type-definition records appended
// after the flat symbol list in a Bin/Tx/Symbols payload. Returns
// definitions keyed by type name. Mirrors tcanalytics4j's
// SymbolTypeMetadataParser.parse().
func ParseTypeMetadata(metadata []byte) (map[string]TypeDefinition, error) {
	definitions := make(map[string]TypeDefinition)
	cursor := 0
	for cursor+4 <= len(metadata) {
		totalLen := int(binary.LittleEndian.Uint32(metadata[cursor:]))
		if totalLen <= 0 || cursor+totalLen > len(metadata) {
			break
		}

		rec, err := parseTypeRecord(metadata, cursor)
		if err != nil {
			return nil, err
		}
		if rec.recordKind == typeRecordKind {
			definitions[rec.name] = toTypeDefinition(rec)
		}
		cursor = rec.endOffset
	}
	return definitions, nil
}

func toTypeDefinition(rec parsedRecord) TypeDefinition {
	members := make([]TypeMember, 0, len(rec.children))
	for _, child := range rec.children {
		if child.recordKind == fieldRecordKind {
			members = append(members, TypeMember{
				Name:       child.name,
				TypeName:   child.typeName,
				DataType:   child.dataType,
				SizeBytes:  child.sizeBytes,
				ByteOffset: child.offsetOrUnknown,
			})
		}
	}
	return TypeDefinition{
		Name:         rec.name,
		DataType:     rec.dataType,
		SizeBytes:    rec.sizeBytes,
		BaseTypeName: rec.typeName,
		Members:      members,
		EnumValues:   rec.enumValues,
	}
}

// parseTypeRecord decodes one record (type or field) starting at baseOffset
// in data. This is a literal port of SymbolTypeMetadataParser.parseRecord —
// fidelity matters more than idiom here.
//
// Every field read past the initial totalLen is relative to a fresh reslice
// of data (record := data[baseOffset:baseOffset+totalLen]), and recursive
// calls for children pass that reslice as the new base buffer, not the
// original outer buffer. endOffset is the one field that stays in the outer
// buffer's coordinate space, since it's what lets the caller's cursor keep
// advancing correctly. Do not "simplify" this into pure global-offset
// arithmetic — a wrong offset here still produces plausible-looking output
// (wrong member names/sizes), not a crash, so it's easy to ship silently broken.
func parseTypeRecord(data []byte, baseOffset int) (parsedRecord, error) {
	if baseOffset+4 > len(data) {
		return parsedRecord{}, fmt.Errorf("parser: type record truncated at offset %d", baseOffset)
	}
	totalLen := int(binary.LittleEndian.Uint32(data[baseOffset:]))
	if totalLen < 42 || baseOffset+totalLen > len(data) {
		return parsedRecord{}, fmt.Errorf("parser: type record at %d has invalid totalLen=%d", baseOffset, totalLen)
	}
	record := data[baseOffset : baseOffset+totalLen]

	sizeBytes := int(binary.LittleEndian.Uint32(record[16:]))
	offsetOrUnknown := int(binary.LittleEndian.Uint32(record[20:]))
	dataType := AdsDataType(binary.LittleEndian.Uint32(record[24:]))
	rawKind := binary.LittleEndian.Uint16(record[28:])
	recordKind := int(rawKind & 0xFF)
	nameLen := int(binary.LittleEndian.Uint16(record[32:]))
	typeLen := int(binary.LittleEndian.Uint16(record[34:]))
	commentLen := int(binary.LittleEndian.Uint16(record[36:]))
	childCount := int(binary.LittleEndian.Uint16(record[40:]))

	cursor := 42
	name, cursor, err := readNulTermField(record, cursor, nameLen)
	if err != nil {
		return parsedRecord{}, err
	}

	var typeName string
	if typeLen > 0 {
		typeName, cursor, err = readNulTermField(record, cursor, typeLen)
		if err != nil {
			return parsedRecord{}, err
		}
	}

	if commentLen > 0 {
		cursor += commentLen
		if cursor < len(record) && record[cursor] == 0 {
			cursor++
		}
	}

	var children []parsedRecord
	var enumValues []EnumValue
	switch {
	case recordKind == typeRecordKind && childCount > 0:
		cursor = findChildStart(record, cursor, childCount)
		children = make([]parsedRecord, 0, childCount)
		for i := 0; i < childCount; i++ {
			child, err := parseTypeRecord(record, cursor)
			if err != nil {
				return parsedRecord{}, err
			}
			children = append(children, child)
			cursor = child.endOffset
		}
	case recordKind == typeRecordKind && childCount == 0 && typeName != "":
		enumValues, err = parseEnumValues(record, cursor, sizeBytes)
		if err != nil {
			return parsedRecord{}, err
		}
	}

	return parsedRecord{
		endOffset:       baseOffset + totalLen,
		recordKind:      recordKind,
		dataType:        dataType,
		sizeBytes:       sizeBytes,
		offsetOrUnknown: offsetOrUnknown,
		name:            name,
		typeName:        typeName,
		children:        children,
		enumValues:      enumValues,
	}, nil
}

// readNulTermField reads a length-prefixed field from record starting at
// cursor, then skips one trailing NUL byte if present. Returns the decoded
// string and the cursor position after the field (and its NUL, if skipped).
func readNulTermField(record []byte, cursor, length int) (string, int, error) {
	if cursor+length > len(record) {
		return "", 0, fmt.Errorf("parser: type record field exceeds record bounds (cursor=%d len=%d recordLen=%d)",
			cursor, length, len(record))
	}
	s := string(record[cursor : cursor+length])
	cursor += length
	if cursor < len(record) && record[cursor] == 0 {
		cursor++
	}
	return s, cursor, nil
}

// findChildStart locates the first child record within record, starting the
// search at candidateOffset. It probes up to 8 bytes for a record whose
// totalLen looks plausible and whose word at +4 equals 1, falling back to
// candidateOffset if nothing matches. This heuristic is inherited as-is from
// SymbolTypeMetadataParser.findChildStart — fragile but sufficient for the
// captures in hand; not hardened further here.
func findChildStart(record []byte, candidateOffset, childCount int) int {
	if childCount == 0 {
		return candidateOffset
	}
	limit := candidateOffset + 8
	if maxLimit := len(record) - 8; maxLimit < limit {
		limit = maxLimit
	}
	for offset := candidateOffset; offset <= limit; offset++ {
		if offset < 0 || offset+8 > len(record) {
			continue
		}
		totalLen := int(binary.LittleEndian.Uint32(record[offset:]))
		if totalLen <= 0 || offset+totalLen > len(record) {
			continue
		}
		if binary.LittleEndian.Uint32(record[offset+4:]) == 1 {
			return offset
		}
	}
	return candidateOffset
}

// parseEnumValues decodes the enum-value table of a type-aliasing record
// (childCount == 0, typeName != ""). Ported from
// SymbolTypeMetadataParser.parseEnumValues, with one deliberate deviation:
// see readEnumValue.
func parseEnumValues(record []byte, cursor, sizeBytes int) ([]EnumValue, error) {
	alignedCursor := cursor
	if alignedCursor%2 != 0 {
		alignedCursor++
	}
	if alignedCursor+16+2+minEnumPropertyBytes > len(record) {
		return nil, nil
	}

	propCursor := alignedCursor + 16
	propertyCount := int(binary.LittleEndian.Uint16(record[propCursor:]))
	propCursor += 2
	for i := 0; i < propertyCount; i++ {
		if propCursor+2 > len(record) {
			return nil, nil
		}
		propLen := int(binary.LittleEndian.Uint16(record[propCursor:]))
		propCursor += 2 + propLen
		for propCursor < len(record) && record[propCursor] == 0 {
			propCursor++
		}
	}

	if propCursor+2 > len(record) {
		return nil, nil
	}
	enumCount := int(binary.LittleEndian.Uint16(record[propCursor:]))
	propCursor += 2
	if enumCount <= 0 {
		return nil, nil
	}

	valueWidth := sizeBytes
	if valueWidth < 2 {
		valueWidth = 2
	}

	values := make([]EnumValue, 0, enumCount)
	for i := 0; i < enumCount; i++ {
		if propCursor+1 > len(record) {
			return nil, nil
		}
		nameLen := int(record[propCursor])
		propCursor++
		if propCursor+nameLen > len(record) {
			return nil, nil
		}
		name := string(record[propCursor : propCursor+nameLen])
		propCursor += nameLen
		if propCursor < len(record) && record[propCursor] == 0 {
			propCursor++
		}
		if propCursor+valueWidth > len(record) {
			return nil, nil
		}
		numeric := readEnumValue(record[propCursor:], valueWidth)
		propCursor += valueWidth
		values = append(values, EnumValue{Name: name, NumericValue: numeric})
	}
	return values, nil
}

// readEnumValue decodes a width-byte little-endian enum value.
//
// Deliberately NOT a literal port for width==8: the Java reference assembles
// the two 32-bit halves as `((long) low) | ((long) high << 32)`, which sign-
// extends `low` into the long before OR-ing when its MSB is set, corrupting
// the result for ULINT/LWORD-backed enums with a low dword >= 0x80000000.
// This assembles both halves as unsigned before combining, which is simply
// correct. Low practical impact (no known fixture uses an 8-byte enum), but
// worth flagging as an intentional non-literal port.
func readEnumValue(b []byte, width int) int64 {
	switch width {
	case 2:
		return int64(int16(binary.LittleEndian.Uint16(b)))
	case 4:
		return int64(int32(binary.LittleEndian.Uint32(b)))
	case 8:
		lo := uint64(binary.LittleEndian.Uint32(b))
		hi := uint64(binary.LittleEndian.Uint32(b[4:]))
		return int64(hi<<32 | lo)
	default:
		return int64(int16(binary.LittleEndian.Uint16(b)))
	}
}
