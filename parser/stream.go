// Package parser provides a hand-written parser for the TwinCAT Analytics
// Bin/Tx/Symbols MQTT payload (symbol stream binary format).
//
// The wire format is documented in docs/FORMAT_ANALYSIS.md.
package parser

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// AdsDataType is the ADS/TwinCAT primitive data-type identifier as stored in
// the symbol stream.  Values match the ads_data_type enum documented in docs/FORMAT_ANALYSIS.md.
type AdsDataType uint32

const (
	AdsDataTypeVoid     AdsDataType = 0
	AdsDataTypeInt16    AdsDataType = 2
	AdsDataTypeInt32    AdsDataType = 3
	AdsDataTypeReal32   AdsDataType = 4
	AdsDataTypeReal64   AdsDataType = 5
	AdsDataTypeInt8     AdsDataType = 16
	AdsDataTypeUint8    AdsDataType = 17
	AdsDataTypeUint16   AdsDataType = 18
	AdsDataTypeUint32   AdsDataType = 19
	AdsDataTypeInt64    AdsDataType = 20
	AdsDataTypeUint64   AdsDataType = 21
	AdsDataTypeString   AdsDataType = 30
	AdsDataTypeWString  AdsDataType = 31
	AdsDataTypeRead80   AdsDataType = 32
	AdsDataTypeBit      AdsDataType = 33
	AdsDataTypeMaxTypes AdsDataType = 34
	AdsDataTypeBigType  AdsDataType = 65
)

// SymbolStreamFlags holds the decoded stream header flags bitfield from a
// Bin/Tx/Symbols message header.
type SymbolStreamFlags struct {
	IsOnlineChange       bool
	IsTarget64Bit        bool
	AreBaseTypesIncluded bool
	// PerformQSort is an ordering hint from the PLC runtime.
	PerformQSort bool
}

// SymbolStreamHeader holds the parsed fields from the Bin/Tx/Symbols message header.
type SymbolStreamHeader struct {
	MajorVersion uint8
	MinorVersion uint8
	// NumSymbols is the count of symbol entries in the stream.
	NumSymbols uint32
	// CodePage is the code page used for string encoding (typically 65001 = UTF-8).
	CodePage uint32
	// Flags holds the decoded stream header flags.
	Flags SymbolStreamFlags
	// GUID is the layout identifier in canonical wire-byte order.
	// It matches the LayoutGUID field of the corresponding Bin/Tx/Data messages.
	GUID [16]byte
}

// SymbolBody holds the essential fields of a single parsed symbol entry.
type SymbolBody struct {
	// IndexOffset is the raw ADS index offset for this symbol.
	IndexOffset uint32
	// Size is the byte count of this symbol's value in a data sample.
	Size uint32
	// DataType is the ADS primitive data-type identifier.
	DataType AdsDataType
	// IsBitType is true when the symbol_flags is_bit_value bit is set.
	// TwinCAT sets this for BOOL symbols that are bit-packed; the bit position
	// within the byte is encoded in the low bits of IndexOffset.
	IsBitType bool
	// Name is the fully-qualified symbol name, e.g. "Main.var_bool".
	Name string
	// TypeName is the IEC/TwinCAT type string, e.g. "BOOL", "DINT", "STRING(80)".
	TypeName string
}

// SymbolStream is the fully parsed result of a Bin/Tx/Symbols MQTT payload.
type SymbolStream struct {
	Header  SymbolStreamHeader
	Symbols []SymbolBody

	// TypeMetadata is the raw trailing bytes after the flat symbol list
	// (data[lenHeader+lenSymbols:]). Empty if the stream carried none.
	// Parsing this into TypeDefinition/EnumValue records happens lazily in
	// layout.NewLayoutFromStream, not here — see ParseTypeMetadata.
	TypeMetadata []byte
}

// minStreamHeaderSize is the minimum number of bytes required to read the
// fixed symbol stream header (64 bytes).
//
// Wire layout:
//
//	[0]    major_version  (u1)
//	[1]    minor_version  (u1)
//	[2:4]  len_header     (u2 LE) — offset where symbol blob starts
//	[4:8]  num_symbols    (u4 LE)
//	[8:12] len_symbols    (u4 LE)
//	...    (reserved fields)
//	[24:28] code_page     (u4 LE)
//	[28:32] flags         (u4 LE bitfield)
//	[48:64] hash / GUID   (16 raw bytes)
const minStreamHeaderSize = 64

// ParseSymbolStream parses a raw Bin/Tx/Symbols MQTT payload and returns the
// decoded stream header and symbol list.
//
// The data_types section (type-definition records trailing the flat symbol
// list) is captured as SymbolStream.TypeMetadata but not parsed here — that
// stays a pure wire decode with no dependency on the heavier record-parsing
// logic in ParseTypeMetadata, mirroring how the type table is only parsed at
// Layout-construction time.
func ParseSymbolStream(data []byte) (*SymbolStream, error) {
	if len(data) < minStreamHeaderSize {
		return nil, fmt.Errorf("parser: symbol stream too short: %d bytes (need at least %d)",
			len(data), minStreamHeaderSize)
	}

	hdr := parseStreamHeader(data)

	lenHeader := int(binary.LittleEndian.Uint16(data[2:4]))
	lenSymbols := int(binary.LittleEndian.Uint32(data[8:12]))

	if lenHeader < minStreamHeaderSize {
		return nil, fmt.Errorf("parser: len_header=%d is less than minimum %d",
			lenHeader, minStreamHeaderSize)
	}
	if lenHeader+lenSymbols > len(data) {
		return nil, fmt.Errorf("parser: symbol blob out of bounds (len_header=%d, len_symbols=%d, payload=%d)",
			lenHeader, lenSymbols, len(data))
	}

	symbolBlob := data[lenHeader : lenHeader+lenSymbols]
	symbols, err := parseSymbols(symbolBlob, hdr.NumSymbols)
	if err != nil {
		return nil, err
	}

	typeMetadata := data[lenHeader+lenSymbols:]

	return &SymbolStream{Header: hdr, Symbols: symbols, TypeMetadata: typeMetadata}, nil
}

// parseStreamHeader reads the fixed symbol stream header fields.
// The caller is responsible for ensuring len(data) >= minStreamHeaderSize.
func parseStreamHeader(data []byte) SymbolStreamHeader {
	rawFlags := binary.LittleEndian.Uint32(data[28:32])
	var hdr SymbolStreamHeader
	hdr.MajorVersion = data[0]
	hdr.MinorVersion = data[1]
	hdr.NumSymbols = binary.LittleEndian.Uint32(data[4:8])
	hdr.CodePage = binary.LittleEndian.Uint32(data[24:28])
	hdr.Flags = SymbolStreamFlags{
		IsOnlineChange:       rawFlags&(1<<0) != 0,
		IsTarget64Bit:        rawFlags&(1<<1) != 0,
		AreBaseTypesIncluded: rawFlags&(1<<2) != 0,
		PerformQSort:         rawFlags&(1<<3) != 0,
	}
	// GUID at bytes [48:64] — raw wire bytes, same order as the data message
	// LayoutGUID field.  No endian conversion needed: the wire bytes are
	// already in the canonical representation used by layout.GUID.
	copy(hdr.GUID[:], data[48:64])
	return hdr
}

// parseSymbols reads numSymbols length-prefixed symbol entries from blob.
//
// Each entry on the wire is:
//
//	u4 len   — total byte count including this field
//	[len-4]  — symbol_body bytes
func parseSymbols(blob []byte, numSymbols uint32) ([]SymbolBody, error) {
	symbols := make([]SymbolBody, 0, int(numSymbols))
	r := bytes.NewReader(blob)

	for i := uint32(0); i < numSymbols; i++ {
		if r.Len() < 4 {
			return nil, fmt.Errorf("parser: truncated symbol list at symbol %d of %d", i, numSymbols)
		}

		var totalLen uint32
		if err := binary.Read(r, binary.LittleEndian, &totalLen); err != nil {
			return nil, fmt.Errorf("parser: read symbol %d length: %w", i, err)
		}
		if totalLen < 4 {
			return nil, fmt.Errorf("parser: symbol %d: invalid totalLen=%d", i, totalLen)
		}

		bodyLen := int(totalLen) - 4
		if bodyLen > r.Len() {
			return nil, fmt.Errorf("parser: symbol %d body length %d exceeds remaining bytes %d",
				i, bodyLen, r.Len())
		}

		bodyBytes := make([]byte, bodyLen)
		if _, err := r.Read(bodyBytes); err != nil {
			return nil, fmt.Errorf("parser: read symbol %d body: %w", i, err)
		}

		sym, err := parseSymbolBody(bodyBytes)
		if err != nil {
			return nil, fmt.Errorf("parser: symbol %d: %w", i, err)
		}
		symbols = append(symbols, sym)
	}
	return symbols, nil
}

// parseSymbolBody decodes a symbol_body from its raw bytes.
//
// symbol_body wire layout (all integers LE):
//
//	[0:4]   index_group   (u4) — not used for layout building
//	[4:8]   index_offset  (u4)
//	[8:12]  len/size      (u4) — byte size of the symbol value
//	[12:16] data_type     (u4) — AdsDataType enum
//	[16:18] symbol_flags  (u2 bitfield)
//	[18:20] len_comment   (u2) — byte count of comment, excluding the +2 padding
//	[20:22] len_name      (u2) — byte count of name, excluding null terminator
//	[22:24] len_type_name (u2) — byte count of type name, excluding null terminator
//	[24 .. 24+len_comment+2]: comment field (null-terminated, with 2-byte padding)
//	[..    .. +len_name+1]:   name (null-terminated UTF-8)
//	[..    .. +len_type_name+1]: type_name (null-terminated UTF-8)
//	[16 trailing bytes]: type_guid (not used)
func parseSymbolBody(b []byte) (SymbolBody, error) {
	const fixedSize = 24
	if len(b) < fixedSize {
		return SymbolBody{}, fmt.Errorf("symbol body too short: %d bytes (need at least %d)", len(b), fixedSize)
	}

	indexOffset := binary.LittleEndian.Uint32(b[4:8])
	size := binary.LittleEndian.Uint32(b[8:12])
	dataType := AdsDataType(binary.LittleEndian.Uint32(b[12:16]))
	// symbol_flags at [16:18] — 16-bit bitfield (see ksy symbol_flags).
	// Bit 1 = is_bit_value: set when this symbol is a bit-packed BOOL.
	symbolFlags := binary.LittleEndian.Uint16(b[16:18])
	isBitType := symbolFlags&(1<<1) != 0
	lenComment := int(binary.LittleEndian.Uint16(b[18:20]))
	lenName := int(binary.LittleEndian.Uint16(b[20:22]))
	lenTypeName := int(binary.LittleEndian.Uint16(b[22:24]))

	// The comment field occupies lenComment+2 bytes (TwinCAT convention: the
	// length does not include a 2-byte overhead — see ksy no_idea_maybe_comment).
	commentEnd := fixedSize + lenComment + 2
	nameEnd := commentEnd + lenName + 1
	typeEnd := nameEnd + lenTypeName + 1

	if typeEnd > len(b) {
		return SymbolBody{}, fmt.Errorf(
			"symbol body string fields exceed body size: typeEnd=%d, bodyLen=%d "+
				"(lenComment=%d, lenName=%d, lenTypeName=%d)",
			typeEnd, len(b), lenComment, lenName, lenTypeName)
	}

	name := nullTermStr(b[commentEnd:nameEnd])
	typeName := nullTermStr(b[nameEnd:typeEnd])

	// b[typeEnd:] contains type_guid (16 bytes, wire order) and is intentionally
	// not parsed — the layout GUID is taken from the stream header Hash field.
	// The entry boundary is enforced by the totalLen prefix in parseSymbols.

	return SymbolBody{
		IndexOffset: indexOffset,
		Size:        size,
		DataType:    dataType,
		IsBitType:   isBitType,
		Name:        name,
		TypeName:    typeName,
	}, nil
}

// nullTermStr returns the string content of b up to the first null byte.
// If there is no null byte, the entire slice is returned as a string.
func nullTermStr(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		return string(b[:i])
	}
	return string(b)
}
