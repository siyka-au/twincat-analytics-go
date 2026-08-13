// TwinCAT Analytics — StructTypeTest: SensorReading type definition
// Part of: plc/StructTypeTest/
// Depends on: NestedData.dut.st
// See StructTypeTest.prg.st for the program that uses this type.

TYPE SensorReading :
STRUCT
    raw     : UDINT;        // Level-1: = master_count
    scaled  : REAL;         // Level-1: = UDINT_TO_REAL(master_count) / 100.0
    signed  : DINT;         // Level-1: = UDINT_TO_DINT(master_count) - 500
    flag    : BOOL;         // Level-1: = (raw > 100)
    label   : STRING(20);   // Level-1: = 'Reading#' + UDINT_TO_STRING(master_count)
    inner   : NestedData;   // Level-2: populated from level-1 fields above
END_STRUCT
END_TYPE