// TwinCAT Analytics — Dynamic Type Test Program
//
// Purpose:
//   A companion to SimpleTypeTest.st. Declares the same scalar numeric
//   variables but adds a cyclic body that mutates them each scan:
//
//     Integers  — increment by 1 every scan, wrap naturally at the type
//                 boundary (IEC modular overflow; no explicit MOD needed).
//
//     REAL      — amplitude-modulated sinusoid (single precision):
//                   A(t)      = sin(2π · t / T_slow)           period 60 s
//                   var_real  = A(t) · sin(2π · t / T_fast)    period  5 s
//
//     LREAL     — same formula, independent accumulator, double precision.
//
//   String, WString, Time, Date, DT and TOD variables are declared with the
//   same init values as SimpleTypeTest.st but are NOT mutated at runtime.

// @runner base-topic: Devices/DynamicTypeTest/CyclicStream_1
// @runner sentinel:   DynamicTypeTest.nMaster

PROGRAM DynamicTypeTest
VAR
    // ── Boolean ───────────────────────────────────────────────────────────────
    var_bool        : BOOL  := TRUE;

    // ── Signed integers ───────────────────────────────────────────────────────
    var_sint        : SINT  := -100;    // wraps  127 → -128
    var_int         : INT   := -1000;   // wraps  32767 → -32768
    var_dint        : DINT  := -100000;
    var_lint        : LINT  := -1000000000;

    // ── Unsigned integers ─────────────────────────────────────────────────────
    var_usint       : USINT := 200;     // wraps  255 → 0
    var_uint        : UINT  := 60000;
    var_udint       : UDINT := 3000000000;
    var_ulint       : ULINT := 10000000000;

    // ── Bit-string types ──────────────────────────────────────────────────────
    var_byte        : BYTE  := 16#AB;
    var_word        : WORD  := 16#ABCD;
    var_dword       : DWORD := 16#DEADBEEF;
    var_lword       : LWORD := 16#DEADBEEFCAFEBABE;

    // ── Floating point ────────────────────────────────────────────────────────
    var_real        : REAL  := 0.0;
    var_lreal       : LREAL := 0.0;

    // ── Strings (static — not mutated at runtime) ─────────────────────────────
    var_string        : STRING      := 'Hello TwinCAT';
    var_string_short  : STRING(10)  := 'Hi';
    var_string_long   : STRING(255) := 'The quick brown fox jumps over the lazy dog. Extra padding to stress-test long string parsing in the binary symbol stream.';

    // ── Wide strings (static) ─────────────────────────────────────────────────
    var_wstring        : WSTRING      := "Hello TwinCAT Wide";
    var_wstring_short  : WSTRING(10)  := "Hi Wide";
    var_wstring_long   : WSTRING(255) := "The quick brown fox jumps over the lazy dog in wide-string format. Testing WSTRING parser alignment across multiple lengths.";

    // ── Time / Date (static) ─────────────────────────────────────────────────
    var_time          : TIME          := T#1S500MS;
    var_date          : DATE          := D#2026-02-23;
    var_date_and_time : DATE_AND_TIME := DT#2026-02-23-10:30:00;
    var_time_of_day   : TIME_OF_DAY   := TOD#10:30:00.000;
    var_ltime         : LTIME         := LTIME#1D2H3M4S5MS;

    // ── Internal accumulators (not intended as analytics outputs) ─────────────
    // Prefix with underscore so they sort after public variables in the stream.
    _t_real         : REAL  := 0.0;   // elapsed time for REAL sinusoid (seconds)
    _t_lreal        : LREAL := 0.0;   // elapsed time for LREAL sinusoid (seconds)

    // Cycle time in seconds — set this to match the actual PLC task cycle.
    // e.g. 10 ms task → 0.01, 1 ms task → 0.001
    _cycle_s        : REAL  := 0.01;
END_VAR

VAR CONSTANT
    _TWO_PI         : REAL  := 6.28318530;           // 2π  (single precision)
    _TWO_PI_LR      : LREAL := 6.28318530717958648;  // 2π  (double precision)
    _T_FAST         : REAL  := 5.0;                  // fast carrier period (s)
    _T_SLOW         : REAL  := 60.0;                 // slow envelope period (s)
END_VAR

// ─────────────────────────────────────────────────────────────────────────────
// Cyclic body — executed once per PLC task cycle
// ─────────────────────────────────────────────────────────────────────────────

// ── Signed integer increments ─────────────────────────────────────────────────
// Overflow is defined by TwinCAT / IEC 61131-3: signed types wrap two's-
// complement style (127 + 1 = -128 for SINT, etc.).
var_sint  := var_sint  + 1;
var_int   := var_int   + 1;
var_dint  := var_dint  + 1;
var_lint  := var_lint  + 1;

// ── Unsigned integer / bit-string increments ──────────────────────────────────
// Unsigned overflow is modular: 255 + 1 = 0 for BYTE/USINT, etc.
var_usint := var_usint + 1;
var_uint  := var_uint  + 1;
var_udint := var_udint + 1;
var_ulint := var_ulint + 1;

var_byte  := var_byte  + 1;
var_word  := var_word  + 1;
var_dword := var_dword + 1;
var_lword := var_lword + 1;

// ── REAL sinusoid  ────────────────────────────────────────────────────────────
// Advance accumulator; subtract T_slow instead of using MOD to avoid rounding
// errors accumulating in the low bits over thousands of cycles.
_t_real := _t_real + _cycle_s;
IF _t_real >= _T_SLOW THEN
    _t_real := _t_real - _T_SLOW;
END_IF

//   envelope(t) = sin(2π · t / T_slow)          range [-1, +1]
//   carrier(t)  = sin(2π · t / T_fast)          period 5 s
//   output(t)   = envelope(t) · carrier(t)
var_real := SIN(_TWO_PI * _t_real / _T_SLOW)
          * SIN(_TWO_PI * _t_real / _T_FAST);

// ── LREAL sinusoid ────────────────────────────────────────────────────────────
_t_lreal := _t_lreal + REAL_TO_LREAL(_cycle_s);
IF _t_lreal >= REAL_TO_LREAL(_T_SLOW) THEN
    _t_lreal := _t_lreal - REAL_TO_LREAL(_T_SLOW);
END_IF

var_lreal := SIN(_TWO_PI_LR * _t_lreal / REAL_TO_LREAL(_T_SLOW))
           * SIN(_TWO_PI_LR * _t_lreal / REAL_TO_LREAL(_T_FAST));