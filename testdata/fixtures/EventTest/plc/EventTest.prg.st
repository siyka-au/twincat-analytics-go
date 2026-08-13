// TwinCAT Analytics — Event-Based Stream Test Program
//
// Purpose:
//   Designed to exercise the IoT Data Agent's Eventbased stream mode
//   (DataFlags bit 0x200).  Most variables remain STATIC for many consecutive
//   scans, so TF6720 publishes far fewer samples than actual PLC cycles.
//   The small number of changing variables make the event-delta stream easy
//   to verify exhaustively.
//
// Determinism contract
//   _cycle_count : UDINT is the internal sequencer (underscore prefix — not
//   intended as an analytics output).
//   Given _cycle_count = C:
//
//   Level-0 (internal only — not published):
//     _cycle_count increments each scan.
//
//   Level-1 (direct from _cycle_count):
//     phase  = UDINT_TO_USINT(C MOD 16)     ∈ {0..15}, period = 16 scans
//
//   Level-2 (derived from phase):
//     event_a      = (phase = 0)            TRUE exactly once per 16 scans
//     event_b      = (phase < 8)            TRUE for first half of each period
//     event_toggle = ((_cycle_count / 16) MOD 2 = 0)
//                    — flips every 16 scans; TRUE for scans 0..15, FALSE 16..31, ...
//     state_label  = CASE (phase MOD 4):
//                      0 → 'IDLE'    (scans 0, 4, 8, 12)
//                      1 → 'ACTIVE'  (scans 1, 5, 9, 13)
//                      2 → 'WAIT'    (scans 2, 6, 10, 14)
//                      3 → 'DONE'    (scans 3, 7, 11, 15)
//
//   Static (never change — maximise event-suppression):
//     static_int  = 42
//     static_real = 2.71828
//     static_str  = 'EVENT_TEST'
//
// Event-suppression pattern:
//   Of the 16 scans in one period, state_label changes 4 times, event_a
//   pulses once (1 scan), event_b changes twice (at scan 0 and scan 8),
//   event_toggle changes once (at scan 0 of even periods only).
//   static_* never trigger an event sample.
//
// Test vectors:
//   C=0:  phase=0,  event_a=T, event_b=T, toggle=T, state='IDLE'
//   C=1:  phase=1,  event_a=F, event_b=T, toggle=T, state='ACTIVE'
//   C=7:  phase=7,  event_a=F, event_b=T, toggle=T, state='DONE'
//   C=8:  phase=8,  event_a=F, event_b=F, toggle=T, state='IDLE'
//   C=15: phase=15, event_a=F, event_b=F, toggle=T, state='DONE'
//   C=16: phase=0,  event_a=T, event_b=T, toggle=F, state='IDLE'
//   C=32: phase=0,  event_a=T, event_b=T, toggle=T, state='IDLE'

// @runner base-topic: Devices/EventTest/CyclicStream_1
// @runner sentinel:   EventTest.nMaster

PROGRAM EventTest
VAR
    // ── Internal cycle sequencer ──────────────────────────────────────────────
    // Underscore prefix: not published as an analytics symbol.
    _cycle_count    : UDINT := 0;

    // ── Level-1 ───────────────────────────────────────────────────────────────
    phase           : USINT;    // 0..15, period 16 scans

    // ── Level-2: event signals ────────────────────────────────────────────────
    // Pulse: TRUE for exactly 1 scan per 16-scan period.
    event_a         : BOOL;
    // Level: TRUE for the first 8 scans (phase 0..7), FALSE for 8..15.
    event_b         : BOOL;
    // Toggle: flips every 16 scans (once per period boundary).
    event_toggle    : BOOL;
    // State label: 4-state rotation, changes every 4 scans.
    state_label     : STRING(8);

    // ── Static variables ──────────────────────────────────────────────────────
    // These never change at runtime — they exist to produce long identical
    // sample sequences in the stream, giving the event-mode filter something
    // to suppress.
    static_int      : INT   := 42;
    static_real     : REAL  := 2.71828;
    static_str      : STRING(16) := 'EVENT_TEST';
END_VAR

// ─────────────────────────────────────────────────────────────────────────────
// Cyclic body
// ─────────────────────────────────────────────────────────────────────────────

// ── Level-0: advance sequencer ────────────────────────────────────────────────
_cycle_count := _cycle_count + 1;

// ── Level-1: phase (0..15) ────────────────────────────────────────────────────
phase := UDINT_TO_USINT(_cycle_count MOD 16);

// ── Level-2: event signals ────────────────────────────────────────────────────

// event_a: single-scan pulse at the start of each 16-scan period.
event_a := (phase = 0);

// event_b: high during the first half of each period (phase 0..7).
event_b := (phase < 8);

// event_toggle: alternates every 16 scans.
// (_cycle_count / 16) gives the period index (integer division).
// Even periods → TRUE, odd periods → FALSE.
event_toggle := ((_cycle_count / 16) MOD 2 = 0);

// state_label: 4-state rotating label, indexed by (phase MOD 4).
CASE (phase MOD 4) OF
    0: state_label := 'IDLE';
    1: state_label := 'ACTIVE';
    2: state_label := 'WAIT';
    3: state_label := 'DONE';
END_CASE

// static_int, static_real, static_str — no assignment; retain init values.