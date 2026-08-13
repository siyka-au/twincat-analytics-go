// TwinCAT Analytics — Conveyor Controller Interleave Test Program
//
// Purpose:
//   One of two programs in plc/InterleaveTest/ designed to run simultaneously
//   on separate PLCs connected to the same MQTT broker.  Together with
//   TemperatureSensorTest.prg.st it exercises the Go parser's ability to
//   handle multiple concurrent symbol streams (different base topics, different
//   GUIDs) arriving interleaved over a single broker connection.
//
//   This program simulates a conveyor belt controller.
//
// Recommended base topic: Devices/ConveyorB
// Companion program:      TemperatureSensorTest.prg.st  (Devices/TempSensorA)
//
// Determinism contract
//   master_count : UDINT is the single source of truth.
//   Given master_count = N:
//
//   Level-1 (direct from master_count):
//     belt_speed_pct = USINT(N MOD 101)     ∈ [0, 100] %
//                      cycles 0→100→0→100…  period = 101 scans
//     total_parts    = N                    cumulative part count (UDINT)
//
//   Level-2 (derived from level-1):
//     running        = (belt_speed_pct > 0)
//     cycle_time_ms  = IF running
//                        THEN 1000.0 / USINT_TO_REAL(belt_speed_pct)
//                        ELSE 0.0
//                      (time per part at current speed; ∞ approached as speed→0)
//     status         = IF running THEN 'RUNNING' ELSE 'STOPPED'
//
//   Static (never change at runtime):
//     station_id     = 'CONVEYOR-B'
//     max_speed_pct  = 100
//
// Test vectors:
//   N=0:   speed=0,   parts=0,   running=F, cycle_ms=0.0,   status='STOPPED'
//   N=1:   speed=1,   parts=1,   running=T, cycle_ms=1000.0,status='RUNNING'
//   N=50:  speed=50,  parts=50,  running=T, cycle_ms=20.0,  status='RUNNING'
//   N=100: speed=100, parts=100, running=T, cycle_ms=10.0,  status='RUNNING'
//   N=101: speed=0,   parts=101, running=F, cycle_ms=0.0,   status='STOPPED'
//   N=102: speed=1,   parts=102, running=T, cycle_ms=1000.0,status='RUNNING'

// @runner base-topic: Devices/ConveyorB/CyclicStream_1
// @runner sentinel:   ConveyorControllerTest.nMaster
// @runner plc-label:  PLC 1

PROGRAM ConveyorControllerTest
VAR
    // ── Master counter ────────────────────────────────────────────────────────
    master_count        : UDINT := 0;

    // ── Level-1 ───────────────────────────────────────────────────────────────
    // Belt speed 0..100 %, cycling with period 101 scans.
    belt_speed_pct      : USINT;
    // Cumulative part count since PLC start.
    total_parts         : UDINT;

    // ── Level-2 ───────────────────────────────────────────────────────────────
    // TRUE whenever the belt is moving.
    running             : BOOL;
    // Time between parts at current speed (ms).  0 when stopped.
    cycle_time_ms       : REAL;
    // Human-readable status string.
    status              : STRING(10);

    // ── Static ────────────────────────────────────────────────────────────────
    station_id          : STRING(20) := 'CONVEYOR-B';
    max_speed_pct       : USINT      := 100;
END_VAR

// ─────────────────────────────────────────────────────────────────────────────
// Cyclic body
// ─────────────────────────────────────────────────────────────────────────────

master_count := master_count + 1;

// ── Level-1 ───────────────────────────────────────────────────────────────────
belt_speed_pct := UDINT_TO_USINT(master_count MOD 101);
total_parts    := master_count;

// ── Level-2 ───────────────────────────────────────────────────────────────────
running := (belt_speed_pct > 0);

IF running THEN
    cycle_time_ms := 1000.0 / USINT_TO_REAL(belt_speed_pct);
    status        := 'RUNNING';
ELSE
    cycle_time_ms := 0.0;
    status        := 'STOPPED';
END_IF

// station_id, max_speed_pct — static; no assignment in body.