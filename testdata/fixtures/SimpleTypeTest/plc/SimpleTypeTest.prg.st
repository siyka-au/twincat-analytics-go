// TwinCAT Analytics — Type Coverage Test Program
//
// Purpose:
//   Declares one variable for every ADS/IEC data type so that the TF6720
//   IoT Data Agent publishes a Symbols message containing all types and a
//   Data stream that exercises every decode path in the Go parser.
//
// Usage:
//   1. Create a new TwinCAT 3 project and add this file as PROGRAM SimpleTypeTest.
//   2. Configure the TF6720 IoT Data Agent to publish:
//        {YourBase}/Bin/Tx/Symbols
//        {YourBase}/Bin/Tx/Data
//   3. Run cmd/capture --project-url <url-to-this-file> to snapshot the
//      Symbols binary and generate the paired YAML fixture.
//   4. Run go test ./... to validate the parser against the fixture.
//
// Variable naming: var_{type} (lower snake_case of the IEC / TwinCAT type name).
// Array variables: var_array_of_{type}.
//
// Excluded (out of scope for this fixture):
//   REFERENCE TO, POINTER TO, interfaces — see user request.
//
// ── Fixture cross-references ─────────────────────────────────────────────────
//
// Each @analytics-fixture block records a capture run whose testdata/ files
// were generated from this program. Blocks are append-only: add a new block
// each time you re-capture rather than overwriting the previous one.
//
// Machine parsing:
//   - Find lines matching ^// @analytics-fixture:(begin|end)
//   - Collect key: value lines between each begin/end pair
//   - Strip leading "//   " and split on the first ": "
//
// Fields in each block:
//   capture_dir    — path to the capture directory relative to this .st file,
//                    e.g. ../../internal/parser/testdata/capture-{timestamp}
//                    Placeholder value "(fill in after capture)" means no capture yet.
//   timestamp      — compact UTC timestamp of the capture run (yyMMddHHmm)
//   captured_at    — ISO 8601 UTC timestamp
//   symbols_sha256 — hex SHA-256 of capture-{timestamp}/symbols.bin
//   desc_sha256    — hex SHA-256 of capture-{timestamp}/desc.json
//
// Files under capture_dir:
//   symbols.yml   — human-editable fixture (YAML)
//   symbols.bin   — raw Symbols binary (SHA-256 = symbols_sha256)
//   desc.json     — raw Desc JSON      (SHA-256 = desc_sha256)
//   data/         — Bin/Tx/Data messages (present when captured)

// @runner base-topic: Devices/SimpleTypeTest/CyclicStream_1
// @runner sentinel:   SimpleTypeTest.var_bool

PROGRAM SimpleTypeTest
VAR
    // ── Boolean ──────────────────────────────────────────────────────────────
    // AdsDataType = Bit (33)
    var_bool : BOOL := TRUE;

    // ── Signed integers ──────────────────────────────────────────────────────
    // AdsDataType = Int8 (16)
    var_sint  : SINT  := -100;
    // AdsDataType = Int16 (2)
    var_int   : INT   := -1000;
    // AdsDataType = Int32 (3)
    var_dint  : DINT  := -100000;
    // AdsDataType = Int64 (20)
    var_lint  : LINT  := -1000000000;

    // ── Unsigned integers ────────────────────────────────────────────────────
    // AdsDataType = Uint8 (17)
    var_usint : USINT := 200;
    // AdsDataType = Uint16 (18)
    var_uint  : UINT  := 60000;
    // AdsDataType = Uint32 (19)
    var_udint : UDINT := 3000000000;
    // AdsDataType = Uint64 (21)
    var_ulint : ULINT := 10000000000;

    // ── Bit-string types ─────────────────────────────────────────────────────
    // These appear as BigType (65) in the symbol stream with a named base type.
    var_byte  : BYTE  := 16#AB;
    var_word  : WORD  := 16#ABCD;
    var_dword : DWORD := 16#DEADBEEF;
    var_lword : LWORD := 16#DEADBEEFCAFEBABE;

    // ── Floating point ───────────────────────────────────────────────────────
    // AdsDataType = Real32 (4)
    var_real  : REAL  := 3.14;
    // AdsDataType = Real64 (5)
    var_lreal : LREAL := 3.141592653589793;

    // ── Strings ──────────────────────────────────────────────────────────────
    // AdsDataType = String (30). Default TwinCAT STRING length is 80 chars.
    // Multiple lengths are declared to exercise 4-byte alignment padding in
    // the binary symbol header (the known TC_SYMBOLHDR_VER_3 alignment issue).
    var_string        : STRING      := 'Hello TwinCAT';
    var_string_short  : STRING(10)  := 'Hi';
    var_string_long   : STRING(255) := 'The quick brown fox jumps over the lazy dog. Extra padding to stress-test long string parsing in the binary symbol stream.';

    // ── Wide strings ─────────────────────────────────────────────────────────
    // AdsDataType = WString (31). Default TwinCAT WSTRING length is 80 chars.
    // WSTRING literals use double-quotes in TwinCAT ST.
    var_wstring        : WSTRING      := "Hello TwinCAT Wide";
    var_wstring_short  : WSTRING(10)  := "Hi Wide";
    var_wstring_long   : WSTRING(255) := "The quick brown fox jumps over the lazy dog in wide-string format. Testing WSTRING parser alignment across multiple lengths.";

    // ── Time types ───────────────────────────────────────────────────────────
    // These map to BigType (65) in the symbol stream; the base type is DWORD
    // (TIME, DATE, TOD, DT) or LWORD (LTIME) at the wire level.
    var_time          : TIME          := T#1S500MS;
    var_date          : DATE          := D#2026-02-23;
    var_date_and_time : DATE_AND_TIME := DT#2026-02-23-10:30:00;
    var_time_of_day   : TIME_OF_DAY   := TOD#10:30:00.000;
    // LTIME requires TC3.1 Build 4022 or later.
    var_ltime         : LTIME         := LTIME#1D2H3M4S5MS;

    // ── Arrays ───────────────────────────────────────────────────────────────
    // 4-element arrays. Values are chosen to span the type range and include
    // edge cases (min, -1, 0, max) where the literal fits within TC3 limits.

    // Boolean
    var_array_of_bool  : ARRAY[0..3] OF BOOL  := [TRUE, FALSE, TRUE, FALSE];

    // Signed integers
    var_array_of_sint  : ARRAY[0..3] OF SINT  := [-128, -1, 0, 127];
    var_array_of_int   : ARRAY[0..3] OF INT   := [-32768, -1, 0, 32767];
    var_array_of_dint  : ARRAY[0..3] OF DINT  := [-2147483648, -1, 0, 2147483647];
    var_array_of_lint  : ARRAY[0..3] OF LINT  := [-1000000000, -1, 0, 1000000000];

    // Unsigned integers
    var_array_of_usint : ARRAY[0..3] OF USINT := [0, 1, 127, 255];
    var_array_of_uint  : ARRAY[0..3] OF UINT  := [0, 1, 32768, 65535];
    var_array_of_udint : ARRAY[0..3] OF UDINT := [0, 1, 2147483648, 4294967295];
    var_array_of_ulint : ARRAY[0..3] OF ULINT := [0, 1, 1000000000, 18000000000];

    // Bit-string
    var_array_of_byte  : ARRAY[0..3] OF BYTE  := [16#00, 16#55, 16#AA, 16#FF];
    var_array_of_word  : ARRAY[0..3] OF WORD  := [16#0000, 16#1234, 16#ABCD, 16#FFFF];
    var_array_of_dword : ARRAY[0..3] OF DWORD := [16#00000000, 16#12345678, 16#DEADBEEF, 16#FFFFFFFF];
    var_array_of_lword : ARRAY[0..3] OF LWORD := [16#0000000000000000, 16#123456789ABCDEF0, 16#DEADBEEFCAFEBABE, 16#FFFFFFFFFFFFFFFF];

    // Floating point
    var_array_of_real  : ARRAY[0..3] OF REAL  := [0.0, 1.0, -1.0, 3.14];
    var_array_of_lreal : ARRAY[0..3] OF LREAL := [0.0, 1.0, -1.0, 3.141592653589793];

    // Strings — all elements share the declared length within one array.
    // AdsDataType = String (30)
    var_array_of_string  : ARRAY[0..3] OF STRING  := ['Alpha', 'Beta', 'Gamma', 'Delta'];
    // AdsDataType = WString (31)
    var_array_of_wstring : ARRAY[0..3] OF WSTRING := ["Alpha Wide", "Beta Wide", "Gamma Wide", "Delta Wide"];

    // Time
    var_array_of_time : ARRAY[0..3] OF TIME := [T#0S, T#1S, T#1M, T#1H];
END_VAR

// ── Program body ─────────────────────────────────────────────────────────────
// No runtime logic is required. The TF6720 IoT Data Agent publishes symbol
// values directly from the process image on every configured cycle, regardless
// of whether the variables are used in program logic.
//
// Variables are initialised with representative values above. On PLC reset
// (%MAIN.var_* will be reloaded from the initialisation values).