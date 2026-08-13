package layout

import (
	"encoding/binary"
	"fmt"
	"time"
)

// minDataHeaderSize is the minimum bytes needed to read the fixed portion of a
// Bin/Tx/Data header (before the optional extended fields).
//
//	1  major_version
//	1  minor_version
//	1  len_header        (total header size; samples start here)
//	1  len_sample_header
//	4  len_data
//	4  cycle_time
//	4  flags  (bit-packed stream_flags, 32-bit LE)
//	16 layout GUID
//	── total 32 bytes
//
// When len_header >= 56 a further 24 bytes follow the GUID:
//
//	8  sample_count (uint64 LE)
//	8  start_time   (Windows FILETIME, uint64 LE)
//	8  stop_time    (Windows FILETIME, uint64 LE)
const minDataHeaderSize = 32

// DataFlags holds the decoded stream_flags bitfield from a data message header.
//
// Bit layout (LE, starting from bit 0):
//
//	0    HeadTimestamp     – a stream-level timestamp precedes the first sample.
//	1    SampleTimestamp   – each sample carries a per-sample timestamp header.
//	2    DCTime            – timestamps are TwinCAT DC nanoseconds (not FILETIME).
//	3    reserved
//	4–6  CompressionMethod – 0 = none, 1 = run-length delta.
//	8    SupportSample     – support (full-snapshot) samples are present.
//	9    Eventbased        – stream is event-driven rather than cyclic.
type DataFlags struct {
	HeadTimestamp     bool
	SampleTimestamp   bool
	DCTime            bool
	CompressionMethod uint8
	SupportSample     bool
	Eventbased        bool
}

// DataMessageHeader holds all parsed fields from a Bin/Tx/Data message header.
type DataMessageHeader struct {
	MajorVersion    uint8
	MinorVersion    uint8
	LenHeader       uint8  // total header byte count; samples start at this offset
	LenSampleHeader uint8  // per-sample header size (8 if SampleTimestamp, else 0)
	LenData         uint32 // per-sample data blob size in bytes
	CycleTime       uint32 // PLC cycle time in 100 ns units
	Flags           DataFlags
	LayoutGUID      GUID // matches SymbolStreamHeader.GUID in the paired Symbols message

	// The following fields are present when LenHeader >= 56.
	SampleCount *uint64    // explicit number of samples in this message
	StartTime   *time.Time // UTC start of the capture window (Windows FILETIME)
	StopTime    *time.Time // UTC end   of the capture window (Windows FILETIME)

	// CycleTimeNs is present when LenHeader >= 64 (format v1.2+).
	// Nanosecond-precision cycle time, superseding CycleTime for dead-reckoning.
	CycleTimeNs *int64
}

// DataSample is one decoded sample from a Bin/Tx/Data message.
type DataSample struct {
	// Timestamp is the per-sample timestamp, present only when
	// DataMessageHeader.Flags.SampleTimestamp is true.
	Timestamp *time.Time

	// Raw is the raw sample data blob.  Pass it to Layout.ParseSample to
	// obtain decoded FieldValues.
	Raw []byte
}

// DataMessage is the fully parsed representation of a single Bin/Tx/Data
// MQTT payload.
type DataMessage struct {
	Header DataMessageHeader

	// HeadTimestamp is the stream-level timestamp consumed from the front of
	// the sample region when DataFlags.HeadTimestamp is true.  It is the
	// timestamp for the first sample when per-sample timestamps are not used.
	HeadTimestamp *time.Time

	Samples []DataSample
}

// ParseDataMessage parses a raw Bin/Tx/Data MQTT payload.
//
// Only the framing is decoded here (header + per-sample slices). Symbol values
// are left as raw bytes; call Layout.ParseSample on each DataSample.Raw to
// decode them.
func ParseDataMessage(payload []byte) (*DataMessage, error) {
	if len(payload) < minDataHeaderSize {
		return nil, fmt.Errorf("layout: data message too short: %d bytes (need at least %d)",
			len(payload), minDataHeaderSize)
	}

	hdr, err := parseDataHeader(payload)
	if err != nil {
		return nil, err
	}

	if int(hdr.LenHeader) > len(payload) {
		return nil, fmt.Errorf("layout: len_header %d exceeds payload length %d",
			hdr.LenHeader, len(payload))
	}

	sampleRegion := payload[hdr.LenHeader:]

	// Consume the stream-level HeadTimestamp if present.
	// This is an 8-byte timestamp at the very start of the sample data region
	// (before any samples) giving the timestamp for the first sample when
	// per-sample timestamps (SampleTimestamp flag) are not in use.
	var headTS *time.Time
	if hdr.Flags.HeadTimestamp {
		if len(sampleRegion) < 8 {
			return nil, fmt.Errorf("layout: HeadTimestamp flag set but only %d bytes remain after header",
				len(sampleRegion))
		}
		ts := readTimestamp(sampleRegion[:8], hdr.Flags.DCTime)
		headTS = &ts
		sampleRegion = sampleRegion[8:]
	}

	samples, err := parseSamples(sampleRegion, hdr)
	if err != nil {
		return nil, err
	}

	return &DataMessage{Header: *hdr, HeadTimestamp: headTS, Samples: samples}, nil
}

// parseDataHeader reads and validates the data message header.
func parseDataHeader(payload []byte) (*DataMessageHeader, error) {
	h := &DataMessageHeader{
		MajorVersion:    payload[0],
		MinorVersion:    payload[1],
		LenHeader:       payload[2],
		LenSampleHeader: payload[3],
		LenData:         binary.LittleEndian.Uint32(payload[4:8]),
		CycleTime:       binary.LittleEndian.Uint32(payload[8:12]),
	}

	// Decode the 32-bit flags field.
	rawFlags := binary.LittleEndian.Uint32(payload[12:16])
	h.Flags = DataFlags{
		HeadTimestamp:     rawFlags&(1<<0) != 0,
		SampleTimestamp:   rawFlags&(1<<1) != 0,
		DCTime:            rawFlags&(1<<2) != 0,
		CompressionMethod: uint8((rawFlags >> 4) & 0x7),
		SupportSample:     rawFlags&(1<<8) != 0,
		Eventbased:        rawFlags&(1<<9) != 0,
	}

	// Layout GUID occupies bytes 16–31 as raw wire bytes.
	guid, err := GUIDFromBytes(payload[16:32])
	if err != nil {
		return nil, fmt.Errorf("layout: parse GUID: %w", err)
	}
	h.LayoutGUID = guid

	// When len_header reports at least 56 bytes, a 24-byte extension block
	// (SampleCount + StartTime + StopTime) immediately follows the GUID.
	// LenHeader is the authoritative field — it is what the firmware actually
	// wrote and is what governs where samples begin.
	if int(h.LenHeader) >= 56 {
		if len(payload) < 56 {
			return nil, fmt.Errorf("layout: len_header=%d but payload only %d bytes",
				h.LenHeader, len(payload))
		}
		count := binary.LittleEndian.Uint64(payload[32:40])
		h.SampleCount = &count

		start := windowsFileTimeToUTC(binary.LittleEndian.Uint64(payload[40:48]))
		h.StartTime = &start

		stop := windowsFileTimeToUTC(binary.LittleEndian.Uint64(payload[48:56]))
		h.StopTime = &stop
	}

	// v1.2+: nanosecond-precision cycle time at bytes [56:64].
	if int(h.LenHeader) >= 64 {
		if len(payload) < 64 {
			return nil, fmt.Errorf("layout: len_header=%d but payload only %d bytes",
				h.LenHeader, len(payload))
		}
		cycleTimeNs := int64(binary.LittleEndian.Uint64(payload[56:64]))
		h.CycleTimeNs = &cycleTimeNs
	}

	return h, nil
}

// parseSamples decodes all DataSamples from sampleData.
// The optional HeadTimestamp bytes have already been stripped by the caller.
func parseSamples(sampleData []byte, hdr *DataMessageHeader) ([]DataSample, error) {
	sampleHeaderLen := int(hdr.LenSampleHeader)
	dataLen := int(hdr.LenData)
	isDCTime := hdr.Flags.DCTime

	if dataLen == 0 {
		return nil, nil
	}

	// Run-length compressed stream: variable-length sample bodies.
	if hdr.Flags.CompressionMethod == 1 {
		return parseCompressedSamples(
			sampleData, sampleHeaderLen, dataLen,
			hdr.SampleCount, isDCTime, hdr.Flags.SampleTimestamp,
		)
	}

	// Uncompressed: fixed stride per sample.
	stride := sampleHeaderLen + dataLen
	var n int
	if hdr.SampleCount != nil {
		n = int(*hdr.SampleCount)
	} else {
		n = len(sampleData) / stride
	}

	samples := make([]DataSample, 0, n)
	offset := 0
	for i := 0; i < n; i++ {
		end := offset + stride
		if end > len(sampleData) {
			break // truncated payload; return what we have
		}

		s := DataSample{}

		if hdr.Flags.SampleTimestamp && sampleHeaderLen >= 8 {
			ts := readTimestamp(sampleData[offset:offset+8], isDCTime)
			s.Timestamp = &ts
		}

		raw := sampleData[offset+sampleHeaderLen : end]
		s.Raw = make([]byte, len(raw))
		copy(s.Raw, raw)

		samples = append(samples, s)
		offset = end
	}

	return samples, nil
}

// WindowsFileTimeToUTC converts a Windows FILETIME (100-nanosecond ticks
// since 1601-01-01 UTC) to a Go time.Time in UTC.
//
// Returns the zero time when ft is zero (TwinCAT often sends 0 when DC time
// is not configured) or when ft predates the Unix epoch.
func WindowsFileTimeToUTC(ft uint64) time.Time {
	return windowsFileTimeToUTC(ft)
}

func windowsFileTimeToUTC(ft uint64) time.Time {
	if ft == 0 {
		return time.Time{}
	}
	// Difference between Windows epoch (1601-01-01) and Unix epoch (1970-01-01)
	// expressed in 100-ns ticks: 11,644,473,600 seconds × 10,000,000 ticks/s.
	const windowsToUnixTicks uint64 = 116_444_736_000_000_000
	if ft < windowsToUnixTicks {
		return time.Time{} // timestamp predates the Unix epoch
	}
	unixTicks := ft - windowsToUnixTicks
	sec := int64(unixTicks / 10_000_000)
	nsec := int64(unixTicks%10_000_000) * 100
	return time.Unix(sec, nsec).UTC()
}

// dcTimeToGoTime converts a TwinCAT DC (Distributed Clock) timestamp to
// a Go time.Time in UTC.
//
// DC timestamps are signed 64-bit nanosecond counts since 2000-01-01 00:00:00 UTC.
// Returns the zero time when dcNs is zero (TwinCAT sends 0 when DC is not configured).
func dcTimeToGoTime(dcNs int64) time.Time {
	if dcNs == 0 {
		return time.Time{}
	}
	// 2000-01-01 00:00:00 UTC expressed as Unix epoch seconds.
	const dc2000UnixSec int64 = 946_684_800
	sec := dc2000UnixSec + dcNs/1_000_000_000
	nsec := dcNs % 1_000_000_000
	return time.Unix(sec, nsec).UTC()
}

// readTimestamp reads an 8-byte timestamp from b[:8] and converts it to time.Time.
// When isDCTime is true the value is a DC nanosecond count (since 2000-01-01);
// otherwise it is a Windows FILETIME (100-ns ticks since 1601-01-01).
func readTimestamp(b []byte, isDCTime bool) time.Time {
	raw := binary.LittleEndian.Uint64(b[:8])
	if isDCTime {
		return dcTimeToGoTime(int64(raw))
	}
	return windowsFileTimeToUTC(raw)
}

// parseCompressedSamples decodes run-length-encoded samples from data.
// sampleHeaderLen is the per-sample timestamp header size (0 or 8).
// dataLen is the uncompressed byte size of each sample's data blob.
func parseCompressedSamples(
	data []byte,
	sampleHeaderLen, dataLen int,
	sampleCount *uint64,
	isDCTime, hasSampleTS bool,
) ([]DataSample, error) {
	var samples []DataSample
	var prev []byte
	offset := 0
	maxCount := -1
	if sampleCount != nil {
		maxCount = int(*sampleCount)
	}

	for offset < len(data) {
		if maxCount >= 0 && len(samples) >= maxCount {
			break
		}

		s := DataSample{}

		// Per-sample timestamp header.
		if hasSampleTS && sampleHeaderLen >= 8 {
			if offset+sampleHeaderLen > len(data) {
				break // truncated
			}
			ts := readTimestamp(data[offset:], isDCTime)
			s.Timestamp = &ts
			offset += sampleHeaderLen
		}

		var raw []byte
		var consumed int
		var err error

		if prev == nil {
			// First sample is always uncompressed (no previous sample to delta against).
			if offset+dataLen > len(data) {
				break
			}
			raw = make([]byte, dataLen)
			copy(raw, data[offset:offset+dataLen])
			consumed = dataLen
		} else {
			raw, consumed, err = decompressRLSample(data[offset:], prev, dataLen)
			if err != nil {
				return nil, fmt.Errorf("layout: RL decompress sample %d: %w", len(samples), err)
			}
		}

		s.Raw = raw
		prev = raw
		offset += consumed
		samples = append(samples, s)
	}
	return samples, nil
}

// decompressRLSample decodes one run-length compressed sample from src,
// producing a dataLen-byte output blob.  Returns the decoded blob and the
// number of src bytes consumed.
//
// Run-length encoding (markers are int16 LE):
//
//	marker == 0 → support sample: the next dataLen bytes are a full snapshot.
//	marker <  0 → literal run: the next abs(marker) bytes are new data.
//	marker >  0 → copy run: copy marker bytes from prev at the current output position.
func decompressRLSample(src []byte, prev []byte, dataLen int) ([]byte, int, error) {
	out := make([]byte, dataLen)
	srcPos := 0
	outPos := 0

	for outPos < dataLen {
		if srcPos+2 > len(src) {
			return nil, srcPos, fmt.Errorf("truncated marker at src[%d], outPos=%d/%d",
				srcPos, outPos, dataLen)
		}
		marker := int16(binary.LittleEndian.Uint16(src[srcPos : srcPos+2]))
		srcPos += 2

		switch {
		case marker == 0:
			// Support sample: full snapshot follows.
			if srcPos+dataLen > len(src) {
				return nil, srcPos, fmt.Errorf("truncated support sample body")
			}
			copy(out, src[srcPos:srcPos+dataLen])
			srcPos += dataLen
			outPos = dataLen

		case marker < 0:
			// Literal run: fresh bytes from the stream.
			n := int(-marker)
			if srcPos+n > len(src) {
				return nil, srcPos, fmt.Errorf("truncated literal run: need %d, have %d",
					n, len(src)-srcPos)
			}
			if outPos+n > dataLen {
				return nil, srcPos, fmt.Errorf("literal run overflows output: outPos=%d n=%d dataLen=%d",
					outPos, n, dataLen)
			}
			copy(out[outPos:], src[srcPos:srcPos+n])
			srcPos += n
			outPos += n

		default: // marker > 0
			// Copy run: copy from previous sample at the same output position.
			n := int(marker)
			if outPos+n > dataLen {
				return nil, srcPos, fmt.Errorf("copy run overflows output: outPos=%d n=%d dataLen=%d",
					outPos, n, dataLen)
			}
			copy(out[outPos:], prev[outPos:outPos+n])
			outPos += n
		}
	}
	return out, srcPos, nil
}
