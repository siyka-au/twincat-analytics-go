// TwinCAT Analytics — Temporal Type Test Program
//
// Purpose:
//   Exercises every temporal IEC 61131-3 type (TIME, TIME_OF_DAY, DATE,
//   DATE_AND_TIME, LTIME) in a deterministic, master-driven pattern so
//   that Go unit tests can assert exact decoded values without needing a
//   live capture.
//
// Determinism contract
//   master_count : UDINT is the single source of truth.
//   Given master_count = N every other variable's value is a closed-form
//   expression of N:
//
//   Level┬1 (direct from master_count):
//     var_time  = N * 100 ms               (TIME,     32-bit, wraps ~49 days)
//     var_tod   = (N * 1000) MOD 86400000  (TOD,      ms since midnight)
//
//   Level┬2 (derived from level-1 variables):
//     var_ltime  = var_time expressed in nanoseconds
//                = N * 100_000_000 ns      (LTIME,    64-bit)
//     var_date   = D#2026-02-26            (DATE,     static anchor ─ test baseline)
//     var_dt     = DT#2026-02-26-00:00:00  (DT,       static anchor ─ test baseline)
//     var_time_str = CONCAT('T=',   TIME_TO_STRING(var_time))
//     var_tod_str  = CONCAT('TOD=', TOD_TO_STRING(var_tod))
//
//   var_date and var_dt are intentionally STATIC (never mutated at runtime):
//   they give Go tests a known anchor to assert DATE/DT decoding without
//   depending on cycle timing.
//
// Test vectors (closed-form, verified manually):
//   N=1:   var_time=T#100MS,        var_ltime=LTIME#100MS,    var_tod=TOD#00:00:01.000
//   N=10:  var_time=T#1S,           var_ltime=LTIME#1S,       var_tod=TOD#00:00:10.000
//   N=36:  var_time=T#3S600MS,      var_ltime=LTIME#3S600MS,  var_tod=TOD#00:01:00.000
//   N=864: var_time=T#1M26S400MS,   var_ltime=LTIME#1M26S400MS, var_tod=TOD#00:14:24.000

// @runner base-topic: Devices/TimerTypeTest/CyclicStream_1
// @runner sentinel:   TimerTypeTest.nMaster

PROGRAM TimerTypeTest
VAR
    // ── Master counter ────────────────────────────────────────────────────────
    // Increment: +1 each scan.  Wraps naturally at 2^32 (≈4G scans).
    master_count        : UDINT := 0;

    // ── Level-1: temporal outputs ─────────────────────────────────────────────
    // var_time = master_count * 100 ms
    // UDINT_TO_TIME interprets the argument as milliseconds.
    var_time            : TIME;

    // var_tod = (master_count * 1000 ms) MOD 86_400_000 ms
    // Advances 1 second per count; wraps at midnight.
    var_tod             : TIME_OF_DAY;

    // ── Level-2: further derivatives ─────────────────────────────────────────
    // var_ltime is var_time re-expressed in nanoseconds:
    //   TIME stores milliseconds (DWORD); LTIME stores nanoseconds (LWORD).
    //   Conversion: ms → ns × 1_000_000.
    var_ltime           : LTIME;

    // Static anchors — never mutated.  Allow Go tests to assert DATE / DT
    // decoding independently of the cycling logic.
    var_date            : DATE          := D#2026-02-26;
    var_dt              : DATE_AND_TIME := DT#2026-02-26-00:00:00;

    // Level-2 string derivatives: human-readable form of the time variables.
    // TIME_TO_STRING / TOD_TO_STRING are standard TwinCAT conversion functions.
    var_time_str        : STRING(30);
    var_tod_str         : STRING(30);

    // ── Internal accumulator ──────────────────────────────────────────────────
    // Underscore prefix: not intended as an analytics output.
    _ms_per_day         : UDINT := 86400000;   // 24 h × 3600 s × 1000 ms
END_VAR

VAR CONSTANT
    // 1 ms expressed in nanoseconds − used in the LTIME derivation.
    _NS_PER_MS          : LWORD := 1000000;
END_VAR

// ─────────────────────────────────────────────────────────────────────────────
// Cyclic body
// ─────────────────────────────────────────────────────────────────────────────

// Advance master counter.
master_count := master_count + 1;

// ── Level-1: TIME ─────────────────────────────────────────────────────────────
// N * 100 ms.  Wraps at UDINT overflow (~4G ms ≈ 49.7 days).
var_time := UDINT_TO_TIME(master_count * 100);

// ── Level-1: TOD ──────────────────────────────────────────────────────────────
// N * 1000 ms, taken modulo one day so the value always sits in [0, 86399999].
var_tod := UDINT_TO_TOD((master_count * 1000) MOD _ms_per_day);

// ── Level-2: LTIME ────────────────────────────────────────────────────────────
// Convert var_time (milliseconds stored in a DWORD) to nanoseconds (LWORD),
// then re-interpret as LTIME.
var_ltime := LWORD_TO_LTIME(
    DWORD_TO_LWORD(TIME_TO_DWORD(var_time)) * _NS_PER_MS
);

// ── Level-2: STRING derivatives ───────────────────────────────────────────────
var_time_str := CONCAT('T=',   TIME_TO_STRING(var_time));
var_tod_str  := CONCAT('TOD=', TOD_TO_STRING(var_tod));

// var_date and var_dt are static — no assignment in body.