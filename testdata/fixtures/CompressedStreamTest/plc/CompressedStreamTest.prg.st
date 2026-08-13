// TwinCAT Analytics — RL-Compressed Stream Test Program
//
// Purpose:
//   Produces a data stream where consecutive sample blocks are mostly
//   identical, giving the IoT Data Agent's run-length (RL) compression
//   the maximum opportunity to produce short compressed output.
//
//   50 consecutive samples share bit-for-bit identical data each time
//   slow_count advances — the agent compresses those 50 identical blocks
//   into a single (value, run-length) pair.
//
// Determinism contract
//   _cycle_count : UDINT is the internal sequencer (underscore prefix
//   — not intended as an analytics output).
//
//   Level-0 (internal):
//     _cycle_count increments each scan.
//
//   Level-1 (direct from _cycle_count, updates once every 50 scans):
//     slow_count = number of times (_cycle_count MOD 50 = 49), i.e.
//                  FLOOR(_cycle_count / 50)   (UDINT; 0 at start)
//
//   Level-2 (derived from slow_count):
//     half_count  = UDINT_TO_UINT(slow_count / 2)
//                   — advances every 2 slow_count steps
//     parity      = (slow_count MOD 2 = 0)
//                   — alternates TRUE/FALSE each slow_count step
//     slow_byte   = UDINT_TO_BYTE(slow_count MOD 256)
//                   — cycles 0..255 then wraps
//     label       = CONCAT('Slow#', UDINT_TO_STRING(slow_count))
//                   — e.g. 'Slow#0', 'Slow#1', 'Slow#2', ...
//
//   Static (never change — produce maximum run-length sequences):
//     static_real = 3.14159
//     static_int  = 42
//     static_str  = 'TwinCAT'
//
// Test vectors:
//   C=0..49:   slow_count=0, half=0, parity=T, byte=0x00, label='Slow#0'
//   C=50..99:  slow_count=1, half=0, parity=F, byte=0x01, label='Slow#1'
//   C=100..149:slow_count=2, half=1, parity=T, byte=0x02, label='Slow#2'
//   C=200..249:slow_count=4, half=2, parity=T, byte=0x04, label='Slow#4'
//   C=510..559:slow_count=10,half=5, parity=T, byte=0x0A, label='Slow#10'

// @runner base-topic: Devices/CompressedStreamTest/CyclicStream_1
// @runner sentinel:   CompressedStreamTest.nMaster

PROGRAM CompressedStreamTest
VAR
    // ── Internal cycle sequencer ──────────────────────────────────────────────
    _cycle_count    : UDINT := 0;

    // ── Level-1: slow counter ─────────────────────────────────────────────────
    // Advances by 1 every 50 scans.  Between advances, all published
    // variables are identical → each run of 50 produces one RL-pair.
    slow_count      : UDINT := 0;

    // ── Level-2 derivatives ───────────────────────────────────────────────────
    // half_count: UINT, advances every 2 slow_count steps.
    half_count      : UINT;
    // parity: alternates TRUE / FALSE each slow_count step.
    parity          : BOOL;
    // slow_byte: BYTE, wraps 0..255 each slow_count step.
    slow_byte       : BYTE;
    // label: human-readable slow_count as STRING.
    label           : STRING(20);

    // ── Static variables ──────────────────────────────────────────────────────
    // These NEVER change.  The RL compression engine records them once and
    // then emits only a run-length for subsequent identical samples.
    static_real     : REAL   := 3.14159;
    static_int      : INT    := 42;
    static_str      : STRING(16) := 'TwinCAT';
END_VAR

VAR CONSTANT
    // Number of scans between slow_count advances.
    _SLOW_PERIOD    : UDINT := 50;
END_VAR

// ─────────────────────────────────────────────────────────────────────────────
// Cyclic body
// ─────────────────────────────────────────────────────────────────────────────

// ── Level-0: advance internal sequencer ──────────────────────────────────────
_cycle_count := _cycle_count + 1;

// ── Level-1: advance slow_count once every _SLOW_PERIOD scans ────────────────
IF (_cycle_count MOD _SLOW_PERIOD) = 0 THEN
    slow_count := slow_count + 1;
END_IF

// ── Level-2 derivatives ───────────────────────────────────────────────────────

half_count := UDINT_TO_UINT(slow_count / 2);

parity     := (slow_count MOD 2 = 0);

slow_byte  := UDINT_TO_BYTE(slow_count MOD 256);

label      := CONCAT('Slow#', UDINT_TO_STRING(slow_count));

// static_real, static_int, static_str — no assignment; retain init values.