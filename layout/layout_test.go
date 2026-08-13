package layout_test

import (
	"encoding/binary"
	"math"
	"testing"
	"time"

	"github.com/siyka-au/twincat-analytics-go/layout"
	"github.com/siyka-au/twincat-analytics-go/parser"
)

// ── GUID ─────────────────────────────────────────────────────────────────────

func TestGUID_String(t *testing.T) {
	// Wire bytes for {C57B0B7C-A64D-E12B-C131-4F4819C99615} as observed in
	// the capture-202602231300 fixtures. Verifies that String() correctly
	// interprets the canonical wire-byte order.
	g := layout.GUID{
		0x7C, 0x0B, 0x7B, 0xC5, // Data1 LE → C57B0B7C
		0x4D, 0xA6, // Data2 LE → A64D
		0x2B, 0xE1, // Data3 LE → E12B
		0xC1, 0x31, // Data4 BE → C131
		0x4F, 0x48, 0x19, 0xC9, 0x96, 0x15, // Data4a BE → 4F4819C99615
	}
	want := "{C57B0B7C-A64D-E12B-C131-4F4819C99615}"
	if got := g.String(); got != want {
		t.Errorf("GUID.String(): got %s, want %s", got, want)
	}
}

func TestGUIDFromBytes_TooShort(t *testing.T) {
	if _, err := layout.GUIDFromBytes([]byte{1, 2, 3}); err == nil {
		t.Error("expected error for short byte slice, got nil")
	}
}

func TestGUIDIsZero(t *testing.T) {
	var z layout.GUID
	if !z.IsZero() {
		t.Error("zero GUID should be zero")
	}
	z[0] = 1
	if z.IsZero() {
		t.Error("non-zero GUID should not be zero")
	}
}

// ── Layout ────────────────────────────────────────────────────────────────────

// buildLayout constructs a synthetic Layout from a slice of Field descriptors
// without requiring a real TwincatIotSymbolStream. RelativeOffset is computed
// from IndexOffset − min(IndexOffset).
func buildLayout(fields []layout.Field) *layout.Layout {
	base := fields[0].IndexOffset
	for _, f := range fields[1:] {
		if f.IndexOffset < base {
			base = f.IndexOffset
		}
	}
	patched := make([]layout.Field, len(fields))
	var totalSize uint32
	for i, f := range fields {
		f.RelativeOffset = f.IndexOffset - base
		if end := f.RelativeOffset + f.Size; end > totalSize {
			totalSize = end
		}
		patched[i] = f
	}
	return &layout.Layout{
		Fields:         patched,
		BaseOffset:     base,
		SampleDataSize: totalSize,
	}
}

func TestParseSample_PrimitiveTypes(t *testing.T) {
	const base uint32 = 0x100
	fields := []layout.Field{
		{Name: "b", IndexOffset: base + 0, Size: 1, DataType: parser.AdsDataTypeBit},
		{Name: "i", IndexOffset: base + 1, Size: 4, DataType: parser.AdsDataTypeInt32},
		{Name: "f", IndexOffset: base + 5, Size: 4, DataType: parser.AdsDataTypeReal32},
	}
	l := buildLayout(fields)

	sample := make([]byte, 9)
	sample[0] = 0x01
	negInt32 := int32(-42)
	binary.LittleEndian.PutUint32(sample[1:5], uint32(negInt32))
	binary.LittleEndian.PutUint32(sample[5:9], math.Float32bits(3.14))

	vals := l.ParseSample(sample)
	if len(vals) != 3 {
		t.Fatalf("expected 3 values, got %d", len(vals))
	}
	if v, ok := vals[0].Value.(bool); !ok || !v {
		t.Errorf("bool: got %v, want true", vals[0].Value)
	}
	if v, ok := vals[1].Value.(int32); !ok || v != -42 {
		t.Errorf("int32: got %v, want -42", vals[1].Value)
	}
	if v, ok := vals[2].Value.(float32); !ok || absf32(v-3.14) > 0.001 {
		t.Errorf("float32: got %v, want ~3.14", vals[2].Value)
	}
}

func TestParseSample_StringAndWString(t *testing.T) {
	fields := []layout.Field{
		{Name: "s", IndexOffset: 0, Size: 10, DataType: parser.AdsDataTypeString},
		{Name: "w", IndexOffset: 10, Size: 10, DataType: parser.AdsDataTypeWString},
	}
	l := buildLayout(fields)

	sample := make([]byte, 20)
	copy(sample[0:], []byte("Hi\x00"))
	// UCS-2 LE "OK"
	sample[10] = 'O'
	sample[11] = 0
	sample[12] = 'K'
	sample[13] = 0

	vals := l.ParseSample(sample)
	if len(vals) != 2 {
		t.Fatalf("expected 2 values, got %d", len(vals))
	}
	if v, ok := vals[0].Value.(string); !ok || v != "Hi" {
		t.Errorf("STRING: got %q, want %q", vals[0].Value, "Hi")
	}
	if v, ok := vals[1].Value.(string); !ok || v != "OK" {
		t.Errorf("WSTRING: got %q, want %q", vals[1].Value, "OK")
	}
}

func TestParseSample_FieldOutOfRange(t *testing.T) {
	fields := []layout.Field{
		{Name: "a", IndexOffset: 0, Size: 4, DataType: parser.AdsDataTypeUint32},
		{Name: "b", IndexOffset: 4, Size: 4, DataType: parser.AdsDataTypeUint32},
	}
	l := buildLayout(fields)

	// Only 4 bytes: "b" falls outside and should be silently skipped.
	vals := l.ParseSample([]byte{0x01, 0x00, 0x00, 0x00})
	if len(vals) != 1 {
		t.Fatalf("expected 1 value, got %d", len(vals))
	}
	if v, ok := vals[0].Value.(uint32); !ok || v != 1 {
		t.Errorf("uint32: got %v, want 1", vals[0].Value)
	}
}

func TestParseSample_AllIntegerTypes(t *testing.T) {
	fields := []layout.Field{
		{Name: "i8", IndexOffset: 0, Size: 1, DataType: parser.AdsDataTypeInt8},
		{Name: "u8", IndexOffset: 1, Size: 1, DataType: parser.AdsDataTypeUint8},
		{Name: "i16", IndexOffset: 2, Size: 2, DataType: parser.AdsDataTypeInt16},
		{Name: "u16", IndexOffset: 4, Size: 2, DataType: parser.AdsDataTypeUint16},
		{Name: "i64", IndexOffset: 6, Size: 8, DataType: parser.AdsDataTypeInt64},
		{Name: "u64", IndexOffset: 14, Size: 8, DataType: parser.AdsDataTypeUint64},
	}
	l := buildLayout(fields)

	sample := make([]byte, 22)
	i8val := int8(-5)
	sample[0] = byte(i8val)
	sample[1] = 200
	i16val := int16(-1000)
	binary.LittleEndian.PutUint16(sample[2:4], uint16(i16val))
	binary.LittleEndian.PutUint16(sample[4:6], 60000)
	i64val := int64(-1_000_000_000)
	binary.LittleEndian.PutUint64(sample[6:14], uint64(i64val))
	binary.LittleEndian.PutUint64(sample[14:22], 10_000_000_000)

	vals := l.ParseSample(sample)
	if len(vals) != 6 {
		t.Fatalf("expected 6 values, got %d", len(vals))
	}
	if v, ok := vals[0].Value.(int8); !ok || v != -5 {
		t.Errorf("int8: got %v, want -5", vals[0].Value)
	}
	if v, ok := vals[1].Value.(uint8); !ok || v != 200 {
		t.Errorf("uint8: got %v, want 200", vals[1].Value)
	}
	if v, ok := vals[2].Value.(int16); !ok || v != -1000 {
		t.Errorf("int16: got %v, want -1000", vals[2].Value)
	}
	if v, ok := vals[3].Value.(uint16); !ok || v != 60000 {
		t.Errorf("uint16: got %v, want 60000", vals[3].Value)
	}
	if v, ok := vals[4].Value.(int64); !ok || v != -1_000_000_000 {
		t.Errorf("int64: got %v, want -1000000000", vals[4].Value)
	}
	if v, ok := vals[5].Value.(uint64); !ok || v != 10_000_000_000 {
		t.Errorf("uint64: got %v, want 10000000000", vals[5].Value)
	}
}

// ── Registry ──────────────────────────────────────────────────────────────────

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := layout.NewRegistry()

	g := layout.GUID{1, 2, 3, 4}
	l := &layout.Layout{GUID: g}
	r.Register(l)

	got, ok := r.Get(g)
	if !ok || got != l {
		t.Error("registry did not return the registered layout")
	}
	if r.Len() != 1 {
		t.Errorf("Len: got %d, want 1", r.Len())
	}
	if _, ok := r.Get(layout.GUID{99}); ok {
		t.Error("expected miss for unknown GUID")
	}
}

func TestRegistry_Replace(t *testing.T) {
	r := layout.NewRegistry()
	g := layout.GUID{5}

	l1 := &layout.Layout{GUID: g}
	l2 := &layout.Layout{GUID: g}
	r.Register(l1)
	r.Register(l2)

	got, _ := r.Get(g)
	if got != l2 {
		t.Error("re-register should replace old layout")
	}
	if r.Len() != 1 {
		t.Errorf("Len after replace: got %d, want 1", r.Len())
	}
}

// ── PendingQueue ──────────────────────────────────────────────────────────────

func TestPendingQueue_EnqueueDrain(t *testing.T) {
	q := layout.NewPendingQueue(10)
	g := layout.GUID{7}

	for _, b := range []byte{1, 2, 3} {
		q.Enqueue(g, []byte{b})
	}

	if n := q.Len(); n != 3 {
		t.Errorf("Len before drain: got %d, want 3", n)
	}

	items := q.Drain(g)
	if len(items) != 3 {
		t.Fatalf("Drain: got %d items, want 3", len(items))
	}
	for i, want := range []byte{1, 2, 3} {
		if items[i][0] != want {
			t.Errorf("item %d: got %d, want %d", i, items[i][0], want)
		}
	}

	if n := q.Len(); n != 0 {
		t.Errorf("Len after drain: got %d, want 0", n)
	}
	if q.Drain(g) != nil {
		t.Error("second drain should return nil")
	}
}

func TestPendingQueue_CapEvictsOldest(t *testing.T) {
	q := layout.NewPendingQueue(2)
	g := layout.GUID{1}

	if d := q.Enqueue(g, []byte{1}); d {
		t.Error("first enqueue should not drop")
	}
	if d := q.Enqueue(g, []byte{2}); d {
		t.Error("second enqueue should not drop")
	}
	if d := q.Enqueue(g, []byte{3}); !d {
		t.Error("third enqueue should report a drop")
	}

	items := q.Drain(g)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0][0] != 2 || items[1][0] != 3 {
		t.Errorf("expected [2 3] after eviction, got [%d %d]", items[0][0], items[1][0])
	}
}

func TestPendingQueue_MultipleGUIDs(t *testing.T) {
	q := layout.NewPendingQueue(10)
	g1 := layout.GUID{1}
	g2 := layout.GUID{2}

	q.Enqueue(g1, []byte{10})
	q.Enqueue(g2, []byte{20})
	q.Enqueue(g1, []byte{11})

	if n := q.GUIDCount(); n != 2 {
		t.Errorf("GUIDCount: got %d, want 2", n)
	}

	items1 := q.Drain(g1)
	if len(items1) != 2 {
		t.Errorf("g1 drain: got %d items, want 2", len(items1))
	}
	if n := q.GUIDCount(); n != 1 {
		t.Errorf("GUIDCount after g1 drain: got %d, want 1", n)
	}
}

// ── ParseDataMessage ──────────────────────────────────────────────────────────

func makeDataHeader(major, minor, lenHeader, lenSampleHeader uint8, lenData, cycleTime, flags uint32, guid layout.GUID) []byte {
	b := make([]byte, 32)
	b[0] = major
	b[1] = minor
	b[2] = lenHeader
	b[3] = lenSampleHeader
	binary.LittleEndian.PutUint32(b[4:8], lenData)
	binary.LittleEndian.PutUint32(b[8:12], cycleTime)
	binary.LittleEndian.PutUint32(b[12:16], flags)
	copy(b[16:32], guid[:])
	return b
}

func TestParseDataMessage_V10_NoTimestamp(t *testing.T) {
	const dataLen = 4
	hdr := makeDataHeader(1, 0, 32, 0, dataLen, 1000, 0, layout.GUID{})

	payload := append(hdr, make([]byte, 2*dataLen)...)
	binary.LittleEndian.PutUint32(payload[32:36], 1)
	binary.LittleEndian.PutUint32(payload[36:40], 2)

	msg, err := layout.ParseDataMessage(payload)
	if err != nil {
		t.Fatalf("ParseDataMessage: %v", err)
	}
	if msg.Header.MajorVersion != 1 || msg.Header.MinorVersion != 0 {
		t.Errorf("version: got %d.%d, want 1.0", msg.Header.MajorVersion, msg.Header.MinorVersion)
	}
	if len(msg.Samples) != 2 {
		t.Fatalf("samples: got %d, want 2", len(msg.Samples))
	}
	if msg.Samples[0].Timestamp != nil {
		t.Error("sample 0 should have no timestamp")
	}
	if v := binary.LittleEndian.Uint32(msg.Samples[0].Raw); v != 1 {
		t.Errorf("sample 0 raw: got %d, want 1", v)
	}
	if v := binary.LittleEndian.Uint32(msg.Samples[1].Raw); v != 2 {
		t.Errorf("sample 1 raw: got %d, want 2", v)
	}
}

func TestParseDataMessage_V10_SampleTimestamp(t *testing.T) {
	const dataLen = 4
	const sampleHdrLen = 8
	// flags bit 1 = SampleTimestamp
	hdr := makeDataHeader(1, 0, 32, sampleHdrLen, dataLen, 0, 0x02, layout.GUID{})

	payload := append(hdr, make([]byte, 2*(sampleHdrLen+dataLen))...)

	// Sample 0
	binary.LittleEndian.PutUint64(payload[32:40], 132000000000000000)
	binary.LittleEndian.PutUint32(payload[40:44], 99)
	// Sample 1
	binary.LittleEndian.PutUint64(payload[44:52], 132000001000000000)
	binary.LittleEndian.PutUint32(payload[52:56], 100)

	msg, err := layout.ParseDataMessage(payload)
	if err != nil {
		t.Fatalf("ParseDataMessage: %v", err)
	}
	if len(msg.Samples) != 2 {
		t.Fatalf("samples: got %d, want 2", len(msg.Samples))
	}
	for i, s := range msg.Samples {
		if s.Timestamp == nil || s.Timestamp.IsZero() {
			t.Errorf("sample %d: expected non-zero timestamp", i)
		}
	}
	if v := binary.LittleEndian.Uint32(msg.Samples[0].Raw); v != 99 {
		t.Errorf("sample 0 data: got %d, want 99", v)
	}
}

func TestParseDataMessage_TooShort(t *testing.T) {
	if _, err := layout.ParseDataMessage([]byte{1, 2, 3}); err == nil {
		t.Error("expected error for truncated payload")
	}
}

func TestParseDataMessage_V12_ExplicitSampleCount(t *testing.T) {
	const dataLen = 4
	// v1.2: 64-byte header (as observed in real captures)
	hdr := make([]byte, 64)
	hdr[0] = 1
	hdr[1] = 2
	hdr[2] = 64
	hdr[3] = 8 // len_sample_header (per-sample timestamp present)
	binary.LittleEndian.PutUint32(hdr[4:8], dataLen)
	binary.LittleEndian.PutUint64(hdr[32:40], 1)                  // sample count
	binary.LittleEndian.PutUint64(hdr[40:48], 132000000000000000) // start_time

	const sampleHeaderLen = 8
	payload := append(hdr, make([]byte, sampleHeaderLen+dataLen)...)
	binary.LittleEndian.PutUint32(payload[64+sampleHeaderLen:], 77)

	msg, err := layout.ParseDataMessage(payload)
	if err != nil {
		t.Fatalf("ParseDataMessage v1.2: %v", err)
	}
	if msg.Header.SampleCount == nil || *msg.Header.SampleCount != 1 {
		t.Errorf("SampleCount: got %v, want 1", msg.Header.SampleCount)
	}
	if msg.Header.StartTime == nil {
		t.Error("StartTime should not be nil for v1.2")
	}
	if y := msg.Header.StartTime.Year(); y != 2019 {
		t.Errorf("StartTime year: got %d, want 2019", y)
	}
	if len(msg.Samples) != 1 {
		t.Fatalf("samples: got %d, want 1", len(msg.Samples))
	}
	if v := binary.LittleEndian.Uint32(msg.Samples[0].Raw); v != 77 {
		t.Errorf("sample 0 data: got %d, want 77", v)
	}
}

func TestParseDataMessage_LayoutGUID_Preserved(t *testing.T) {
	wantGUID := layout.GUID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	hdr := makeDataHeader(1, 0, 32, 0, 0, 0, 0, wantGUID)

	msg, err := layout.ParseDataMessage(hdr) // zero samples (len_data=0)
	if err != nil {
		t.Fatalf("ParseDataMessage: %v", err)
	}
	if msg.Header.LayoutGUID != wantGUID {
		t.Errorf("LayoutGUID: got %v, want %v", msg.Header.LayoutGUID, wantGUID)
	}
}

// ── Temporal types ────────────────────────────────────────────────────────────

func TestParseSample_TemporalTypes(t *testing.T) {
	const base uint32 = 0x200
	fields := []layout.Field{
		{Name: "t_time", IndexOffset: base + 0, Size: 4, DataType: parser.AdsDataTypeUint32, TypeName: "TIME"},
		{Name: "t_date", IndexOffset: base + 4, Size: 4, DataType: parser.AdsDataTypeUint32, TypeName: "DATE"},
		{Name: "t_dt", IndexOffset: base + 8, Size: 4, DataType: parser.AdsDataTypeUint32, TypeName: "DATE_AND_TIME"},
		{Name: "t_tod", IndexOffset: base + 12, Size: 4, DataType: parser.AdsDataTypeUint32, TypeName: "TIME_OF_DAY"},
		{Name: "t_ltime", IndexOffset: base + 16, Size: 8, DataType: parser.AdsDataTypeInt64, TypeName: "LTIME"},
	}
	l := buildLayout(fields)

	sample := make([]byte, 24)
	// TIME: 1500 ms
	binary.LittleEndian.PutUint32(sample[0:4], 1500)
	// DATE: 1 day since 1970-01-01 → 1970-01-02
	binary.LittleEndian.PutUint32(sample[4:8], 86400)
	// DATE_AND_TIME: 3600 seconds → 1970-01-01 01:00:00 UTC
	binary.LittleEndian.PutUint32(sample[8:12], 3600)
	// TIME_OF_DAY: 7200000 ms = 2 hours
	binary.LittleEndian.PutUint32(sample[12:16], 7_200_000)
	// LTIME: 2,000,000,000 ns = 2 s
	binary.LittleEndian.PutUint64(sample[16:24], 2_000_000_000)

	vals := l.ParseSample(sample)
	if len(vals) != 5 {
		t.Fatalf("expected 5 values, got %d", len(vals))
	}

	if v, ok := vals[0].Value.(time.Duration); !ok || v != 1500*time.Millisecond {
		t.Errorf("TIME: got %v (%T), want 1.5s", vals[0].Value, vals[0].Value)
	}
	wantDate := time.Unix(86400, 0).UTC()
	if v, ok := vals[1].Value.(time.Time); !ok || !v.Equal(wantDate) {
		t.Errorf("DATE: got %v (%T), want %v", vals[1].Value, vals[1].Value, wantDate)
	}
	wantDT := time.Unix(3600, 0).UTC()
	if v, ok := vals[2].Value.(time.Time); !ok || !v.Equal(wantDT) {
		t.Errorf("DT: got %v (%T), want %v", vals[2].Value, vals[2].Value, wantDT)
	}
	if v, ok := vals[3].Value.(time.Duration); !ok || v != 7_200_000*time.Millisecond {
		t.Errorf("TIME_OF_DAY: got %v (%T), want 2h", vals[3].Value, vals[3].Value)
	}
	if v, ok := vals[4].Value.(time.Duration); !ok || v != 2_000_000_000 {
		t.Errorf("LTIME: got %v (%T), want 2s", vals[4].Value, vals[4].Value)
	}
}

// ── IsBitType propagation ─────────────────────────────────────────────────────

func TestNewLayoutFromStream_IsBitType_Propagated(t *testing.T) {
	stream := &parser.SymbolStream{
		Header: parser.SymbolStreamHeader{MajorVersion: 3, NumSymbols: 2},
		Symbols: []parser.SymbolBody{
			{Name: "Main.bool_bit", IndexOffset: 100, Size: 1, DataType: parser.AdsDataTypeBit, IsBitType: true},
			{Name: "Main.bool_byte", IndexOffset: 101, Size: 1, DataType: parser.AdsDataTypeBit, IsBitType: false},
		},
	}
	l := layout.NewLayoutFromStream(stream)
	byName := make(map[string]layout.Field, len(l.Fields))
	for _, f := range l.Fields {
		byName[f.Name] = f
	}
	if !byName["Main.bool_bit"].IsBitType {
		t.Error("Main.bool_bit: IsBitType should be true")
	}
	if byName["Main.bool_byte"].IsBitType {
		t.Error("Main.bool_byte: IsBitType should be false")
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func absf32(f float32) float32 {
	if f < 0 {
		return -f
	}
	return f
}

var _ = time.Time{} // ensure time import is used
