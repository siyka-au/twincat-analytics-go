// TwinCAT Analytics — Struct / Nested Type Test Program
//
// Purpose:
//   Declares two nested struct TYPE definitions and populates one instance
//   from a single master counter each scan.  Exercises the data-type-table
//   parsing path and struct sub-field layout expansion in the Go parser.
//
// Determinism contract
//   master_count : UDINT is the single source of truth.
//   Given master_count = N:
//
//   Level┬1 (direct from master_count):
//     reading.raw     = N                            (UDINT)
//     reading.scaled  = UDINT_TO_REAL(N) / 100.0    (REAL,  2 decimal places)
//     reading.signed  = UDINT_TO_DINT(N) - 500      (DINT,  negative until N=500)
//     reading.flag    = (N > 100)                    (BOOL)
//     reading.label   = CONCAT('Reading#', UDINT_TO_STRING(N))  (STRING(20))
//
//   Level┬2 (derived from level-1 fields of `reading`):
//     reading.inner.counter = reading.signed * reading.signed
//                           = (N - 500)^2            (DINT,  always ≥ 0 for N≠500)
//     reading.inner.active  = (reading.inner.counter > 0)
//                           = (N ≠ 500)              (BOOL)
//
//   Level┬3 (derived from level-2):
//     summary = CONCAT(reading.label, IF reading.inner.active THEN ' OK' ELSE ' ZERO')
//             = e.g. 'Reading#1 OK', 'Reading#500 ZERO'  (STRING(30))
//
// Test vectors:
//   N=1:   raw=1, scaled=0.01,  signed=-499, flag=F, counter=249001, active=T
//          label='Reading#1', summary='Reading#1 OK'
//   N=100: raw=100,scaled=1.00, signed=-400, flag=F, counter=160000, active=T
//          label='Reading#100', summary='Reading#100 OK'
//   N=101: raw=101,scaled=1.01,signed=-399, flag=T, counter=159201, active=T
//   N=500: raw=500,scaled=5.00,signed=0,   flag=T, counter=0,      active=F
//          label='Reading#500', summary='Reading#500 ZERO'
//   N=501: raw=501,scaled=5.01,signed=1,   flag=T, counter=1,      active=T

// Types: NestedData.dut.st, SensorReading.dut.st
// ─────────────────────────────────────────────────────────────────────────────
// Program
// ─────────────────────────────────────────────────────────────────────────────

// @runner base-topic: Devices/StructTypeTest/CyclicStream_1
// @runner sentinel:   StructTypeTest.nMaster

PROGRAM StructTypeTest
VAR
    // ── Master counter ────────────────────────────────────────────────────────
    master_count    : UDINT := 0;

    // ── Struct instance ───────────────────────────────────────────────────────
    reading         : SensorReading;

    // ── Level-3: summary string derived from reading.label + reading.inner.active
    summary         : STRING(30);
END_VAR

// ─────────────────────────────────────────────────────────────────────────────
// Cyclic body
// ─────────────────────────────────────────────────────────────────────────────

master_count := master_count + 1;

// ── Level-1 fields ─────────────────────────────────────────────────────────
reading.raw    := master_count;
reading.scaled := UDINT_TO_REAL(master_count) / 100.0;
reading.signed := UDINT_TO_DINT(master_count) - 500;
reading.flag   := (reading.raw > 100);
reading.label  := CONCAT('Reading#', UDINT_TO_STRING(master_count));

// ── Level-2 fields (derived from level-1 struct fields) ────────────────────
// counter = signed ^ 2 = (N - 500)^2; always ≥ 0.
reading.inner.counter := reading.signed * reading.signed;
reading.inner.active  := (reading.inner.counter > 0);

// ── Level-3: summary using label (L1) and inner.active (L2) ────────────────
IF reading.inner.active THEN
    summary := CONCAT(reading.label, ' OK');
ELSE
    summary := CONCAT(reading.label, ' ZERO');
END_IF