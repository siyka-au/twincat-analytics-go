// TwinCAT Analytics — Array Access Test Program
//
// Purpose:
//   Exercises per-element array expansion in the Go parser.  Declares
//   16-element numeric arrays (INT and REAL) filled deterministically from
//   a single master counter, plus a static 8-element STRING array.
//   Level-2 aggregates (sum, max) derived from the arrays verify that
//   every element was decoded correctly.
//
// Determinism contract
//   master_count : UDINT is the single source of truth.
//   Given master_count = N:
//
//   Level-1 (direct from master_count):
//     var_array_int[i]  = INT(N * (i + 1))    for i ∈ 0..15
//                         (natural INT wrap-around on overflow)
//     var_array_real[i] = REAL(N) / REAL(i+1) for i ∈ 0..15
//
//   Level-2 (derived from level-1 arrays):
//     sum_int  = SUM of var_array_int[0..15]
//              = N*(1+2+…+16) = N * 136         (DINT, may wrap)
//     max_real = MAX of var_array_real[0..15]
//              = var_array_real[0] = REAL(N)     (float; element[0] always largest
//                                                  since divisor 1 < 2 < … < 16)
//
//   Static (never mutated at runtime):
//     var_array_string[i]  = 'Item' + UDINT_TO_STRING(UDINT(i))
//                            e.g. 'Item0'..'Item7'
//                            — allows Go tests to assert STRING array decoding
//                              without cycle-timing dependency
//
// Test vectors (N = master_count after the increment):
//   N=1:  int[0..15]=[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16]
//         real[0]=1.0, real[1]=0.5, real[15]=0.0625
//         sum_int=136, max_real=1.0
//   N=2:  int=[2,4,6,8,10,12,14,16,18,20,22,24,26,28,30,32]
//         real[0]=2.0, real[1]=1.0
//         sum_int=272, max_real=2.0
//   N=10: int=[10,20,30,...,160]
//         sum_int=1360, max_real=10.0
//   N=240:int[0]=240,int[15]=3600  (3600 fits INT; 241*16=3856 also fits)
//   N=4096:int[0]=4096,int[7]=32768 wraps to -32768 (natural INT overflow)

// @runner base-topic: Devices/ArrayAccessTest/CyclicStream_1
// @runner sentinel:   ArrayAccessTest.nMaster

PROGRAM ArrayAccessTest
VAR
    // ── Master counter ────────────────────────────────────────────────────────
    master_count        : UDINT := 0;

    // ── Level-1: numeric arrays ───────────────────────────────────────────────
    // 16 elements, zero-indexed.
    // var_array_int[i]  = INT(master_count * (i+1))
    var_array_int       : ARRAY[0..15] OF INT;

    // var_array_real[i] = REAL(master_count) / REAL(i+1)
    var_array_real      : ARRAY[0..15] OF REAL;

    // ── Level-2: aggregates derived from arrays ───────────────────────────────
    // Sum of all INT elements.  DINT avoids premature overflow for moderate N.
    sum_int             : DINT;
    // Maximum of all REAL elements.  = var_array_real[0] = REAL(master_count).
    max_real            : REAL;

    // ── Static: string array ──────────────────────────────────────────────────
    // Initialised once; never mutated.  Provides a deterministic STRING array
    // fixture independent of cycle timing.
    var_array_string    : ARRAY[0..7] OF STRING(10) := [
        'Item0', 'Item1', 'Item2', 'Item3',
        'Item4', 'Item5', 'Item6', 'Item7'
    ];

    // ── Internal loop variable (not an analytics output) ─────────────────────
    _i                  : INT := 0;
END_VAR

// ─────────────────────────────────────────────────────────────────────────────
// Cyclic body
// ─────────────────────────────────────────────────────────────────────────────

// Advance master counter.
master_count := master_count + 1;

// ── Level-1: fill integer array ───────────────────────────────────────────────
// Element i = N * (i+1).  INT overflow wraps naturally (two's-complement).
FOR _i := 0 TO 15 DO
    var_array_int[_i] := UDINT_TO_INT(master_count * INT_TO_UDINT(_i + 1));
END_FOR

// ── Level-1: fill real array ──────────────────────────────────────────────────
// Element 0 = N/1.0  (largest), element 15 = N/16.0  (smallest).
FOR _i := 0 TO 15 DO
    var_array_real[_i] := UDINT_TO_REAL(master_count) / INT_TO_REAL(_i + 1);
END_FOR

// ── Level-2: sum of integer array ────────────────────────────────────────────
// sum_int = N * (1+2+…+16) = N * 136
sum_int := 0;
FOR _i := 0 TO 15 DO
    sum_int := sum_int + INT_TO_DINT(var_array_int[_i]);
END_FOR

// ── Level-2: maximum of real array ───────────────────────────────────────────
// By construction element[0] is always the maximum (divisor 1 is the smallest).
// The loop makes the relationship explicit and exercises all 16 elements.
max_real := var_array_real[0];
FOR _i := 1 TO 15 DO
    IF var_array_real[_i] > max_real THEN
        max_real := var_array_real[_i];
    END_IF
END_FOR

// var_array_string — static; no assignment in body.