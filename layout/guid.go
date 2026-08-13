// Package layout provides the layout registry, data-message parser, and
// pending queue used to decode TwinCAT Analytics Bin/Tx/Data messages.
//
// # Overview
//
// When a Bin/Tx/Symbols message arrives the caller parses it with
// parser.ParseSymbolStream, calls NewLayoutFromStream to build a Layout, then
// calls Registry.Register.  The layout GUID is taken from the symbol stream's
// SymbolStreamHeader.GUID field.
//
// Data messages (Bin/Tx/Data) carry the same layout GUID in their own header.
// The caller calls ParseDataMessage to obtain a DataMessage, looks the GUID up
// in the Registry, and uses Layout.ParseSample for each DataSample.Raw blob.
//
// When a data message arrives before its layout is known the raw payload
// should be passed to PendingQueue.Enqueue.  After registering the layout
// call PendingQueue.Drain and reprocess the returned payloads.
package layout

import (
	"encoding/binary"
	"fmt"
)

// GUID is a 16-byte layout identifier, directly comparable and safe to use
// as a map key.  The wire encoding matches TwinCAT's GUID/UUID layout:
// the first three groups are stored little-endian on the wire, and the last
// two groups (data4 / data4a) are stored big-endian.
type GUID [16]byte

// GUIDFromBytes reads exactly 16 bytes and returns a GUID.
// Returns an error if b is shorter than 16 bytes.
func GUIDFromBytes(b []byte) (GUID, error) {
	if len(b) < 16 {
		return GUID{}, fmt.Errorf("layout: GUID requires 16 bytes, got %d", len(b))
	}
	var g GUID
	copy(g[:], b[:16])
	return g, nil
}

// String returns the standard {xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx}
// representation matching TwinCAT's GUID display format.
func (g GUID) String() string {
	data1 := binary.LittleEndian.Uint32(g[0:4])
	data2 := binary.LittleEndian.Uint16(g[4:6])
	data3 := binary.LittleEndian.Uint16(g[6:8])
	grp4 := uint16(g[8])<<8 | uint16(g[9])
	grp5 := uint64(g[10])<<40 | uint64(g[11])<<32 | uint64(g[12])<<24 |
		uint64(g[13])<<16 | uint64(g[14])<<8 | uint64(g[15])
	return fmt.Sprintf("{%08X-%04X-%04X-%04X-%012X}", data1, data2, data3, grp4, grp5)
}

// IsZero reports whether the GUID is the all-zero value.
func (g GUID) IsZero() bool { return g == GUID{} }
