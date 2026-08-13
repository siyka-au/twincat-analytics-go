// TwinCAT Analytics — Temperature Sensor Interleave Test Program
//
// Purpose:
//   One of two programs in plc/InterleaveTest/ designed to run simultaneously
//   on separate PLCs connected to the same MQTT broker.  Together with
//   ConveyorControllerTest.prg.st it exercises the Go parser's ability to
//   handle multiple concurrent symbol streams (different base topics, different
//   GUIDs) arriving interleaved over a single broker connection.
//
//   This program simulates a temperature measurement device.
//
// Recommended base topic: Devices/TempSensorA
// Companion program:      ConveyorControllerTest.prg.st  (Devices/ConveyorB)
//
// Determinism contract
//   master_count : UDINT is the single source of truth.
//   Given master_count = N:
//
//   Level-1 (direct from master_count):
//     temperature_raw     = INT((N MOD 1000) - 500)     ∈ [-500, 499]
//     temperature_celsius = REAL(temperature_raw) / 10.0 ∈ [-50.0, 49.9] °C
//
//   Level-2 (derived from level-1):
//     alarm_high    = (temperature_celsius > 30.0)
//                     TRUE when N MOD 1000 > 800  (i.e. raw > 300 → °C > 30.0)
//     reading_label = CONCAT('T=', REAL_TO_STRING(temperature_celsius), 'C')
//
//   Static (never change at runtime):
//     sensor_id   = 'TEMP-SENSOR-A'
//     unit        = 'degC'
//
// Test vectors:
//   N=1:   raw=-499, celsius=-49.9, alarm=F, label='T=-49.9C'
//   N=500: raw=0,    celsius=0.0,   alarm=F, label='T=0.0C'
//   N=801: raw=301,  celsius=30.1,  alarm=T, label='T=30.1C'
//   N=999: raw=499,  celsius=49.9,  alarm=T, label='T=49.9C'
//   N=1000:raw=-500, celsius=-50.0, alarm=F, label='T=-50.0C'  (wrap)

// @runner base-topic: Devices/TempSensorA/CyclicStream_1
// @runner sentinel:   TemperatureSensorTest.nMaster
// @runner plc-label:  PLC 2

PROGRAM TemperatureSensorTest
VAR
    // ── Master counter ────────────────────────────────────────────────────────
    master_count        : UDINT := 0;

    // ── Level-1 ───────────────────────────────────────────────────────────────
    // Cycles -500..499 repeating; one full cycle = 1000 scans.
    temperature_raw     : INT;
    // Scaled to degrees Celsius with 0.1 °C resolution.
    temperature_celsius : REAL;

    // ── Level-2 ───────────────────────────────────────────────────────────────
    // High-temperature alarm: TRUE when above 30.0 °C.
    alarm_high          : BOOL;
    // Human-readable label string.
    reading_label       : STRING(30);

    // ── Static ────────────────────────────────────────────────────────────────
    sensor_id           : STRING(20) := 'TEMP-SENSOR-A';
    unit                : STRING(8)  := 'degC';
END_VAR

// ─────────────────────────────────────────────────────────────────────────────
// Cyclic body
// ─────────────────────────────────────────────────────────────────────────────

master_count := master_count + 1;

// ── Level-1 ───────────────────────────────────────────────────────────────────
temperature_raw     := UDINT_TO_INT(master_count MOD 1000) - 500;
temperature_celsius := INT_TO_REAL(temperature_raw) / 10.0;

// ── Level-2 ───────────────────────────────────────────────────────────────────
alarm_high    := (temperature_celsius > 30.0);
reading_label := CONCAT('T=', CONCAT(REAL_TO_STRING(temperature_celsius), 'C'));

// sensor_id, unit — static; no assignment in body.