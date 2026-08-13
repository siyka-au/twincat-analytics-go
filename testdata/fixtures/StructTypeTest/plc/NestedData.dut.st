// TwinCAT Analytics — StructTypeTest: NestedData type definition
// Part of: plc/StructTypeTest/
// See StructTypeTest.prg.st for the program that uses this type.

TYPE NestedData :
STRUCT
    // Level-2: (master_count - 500)^2 — always non-negative
    counter : DINT;
    // Level-2: TRUE whenever counter ≠ 0, i.e. master_count ≠ 500
    active  : BOOL;
END_STRUCT
END_TYPE