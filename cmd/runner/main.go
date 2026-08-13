// Command runner is an interactive guided wizard for capturing TwinCAT
// Analytics fixture data. It discovers fixtures from testdata/fixtures/*/fixture.yml,
// lets the user choose a program, and walks through the TwinCAT XAE setup steps
// before capturing.
//
// By default an embedded Mochi MQTT broker is started on :1883 and TwinCAT
// connects to it. Pass --broker <host:port> to use an external broker instead
// (e.g. a Mosquitto instance that TwinCAT is already publishing to).
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	ads "github.com/jarmocluyse/ads-go/pkg/ads"
	mqttbroker "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/siyka-au/twincat-analytics-go/internal/capture"
	"github.com/siyka-au/twincat-analytics-go/internal/fixture"
)

// programEntry describes one fixture discovered from fixture.yml.
type programEntry struct {
	DirName string
	DirPath string
	Streams []streamEntry
	IsMulti bool // true when fixture.yml has a streams: list
}

// streamEntry describes one MQTT stream within a program, populated from fixture.yml.
type streamEntry struct {
	BaseTopic   string // fixture.yml base_topic (overridable with --base-topic for single-stream)
	Sentinel    string // fixture.yml sentinel
	PLCLabel    string // fixture.yml plc_label (empty for single-stream programs)
	ProgramName string // display name, e.g. "ConveyorControllerTest" (multi-stream only)
}

func main() {
	fixturesDir := flag.String("fixtures-dir", "testdata/fixtures", "root directory containing fixture subdirectories")
	broker := flag.String("broker", "", "external MQTT broker address (host:port); when set the embedded Mochi broker is not started")
	brokerAddr := flag.String("broker-addr", ":1883", "embedded MQTT broker listen address (ignored when --broker is set)")
	timeout := flag.Duration("timeout", 60*time.Second, "timeout waiting for Symbols message")
	dataCount := flag.Int("data-count", 0, "number of Data messages to capture (0 = none)")
	dataTimeout := flag.Duration("data-timeout", 0, "data capture time limit (0 = unlimited)")
	baseTopic := flag.String("base-topic", "", "override the MQTT base topic from fixture.yml (single-stream only)")
	plcAddr := flag.String("plc-addr", "", "PLC IP address for ADS sentinel check (e.g. 192.168.1.50); omit to skip")
	mqttClientID := flag.String("mqtt-client-id", "twincat-runner", "MQTT client ID prefix")
	flag.Parse()

	// Collect flags explicitly provided on the command line so that fixture-level
	// capture_defaults only apply to values the user did not explicitly set.
	provided := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) { provided[f.Name] = true })

	// -------------------------------------------------------------------------
	// Broker setup — embedded Mochi (default) or external.
	// -------------------------------------------------------------------------
	if *broker == "" {
		// Start the embedded Mochi broker before doing anything else so TwinCAT
		// can connect as soon as it starts (after the user activates configuration).
		brokerServer := mqttbroker.New(&mqttbroker.Options{})
		if err := brokerServer.AddHook(new(auth.AllowHook), nil); err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: mqtt broker hook: %v\n", err)
			os.Exit(1)
		}
		tcp := listeners.NewTCP(listeners.Config{ID: "t1", Address: *brokerAddr})
		if err := brokerServer.AddListener(tcp); err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: mqtt broker listen on %s: %v\n", *brokerAddr, err)
			os.Exit(1)
		}
		serveErr := make(chan error, 1)
		go func() { serveErr <- brokerServer.Serve() }()
		defer brokerServer.Close()

		// Give the broker a short window to surface any startup error.
		// mochi-mqtt v2 Serve() is non-blocking — it returns nil immediately after
		// spawning listener goroutines, so receiving nil here is the happy path.
		select {
		case err := <-serveErr:
			if err != nil {
				fmt.Fprintf(os.Stderr, "ERROR: mqtt broker failed to start on %s: %v\n", *brokerAddr, err)
				os.Exit(1)
			}
		case <-time.After(200 * time.Millisecond):
			// No error surfaced within the grace period — broker is running.
		}
		fmt.Printf("Embedded MQTT broker started on %s\n\n", *brokerAddr)
	} else {
		fmt.Printf("Using external MQTT broker: %s\n\n", buildBrokerURI(*broker))
	}

	// -------------------------------------------------------------------------
	// Discover PLC programs by scanning testdata/fixtures/*/plc/ for @runner-annotated .prg.st
	// -------------------------------------------------------------------------
	programs, err := discoverPrograms(*fixturesDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: discovering programs in %s: %v\n", *fixturesDir, err)
		os.Exit(1)
	}
	if len(programs) == 0 {
		fmt.Fprintf(os.Stderr, "No fixtures with base_topic configured found under %s/*/fixture.yml\n", *fixturesDir)
		os.Exit(1)
	}

	// Sort alphabetically for a stable display order.
	sort.Slice(programs, func(i, j int) bool {
		return programs[i].DirName < programs[j].DirName
	})

	// -------------------------------------------------------------------------
	// Print numbered selection list
	// -------------------------------------------------------------------------
	fmt.Println("Available TwinCAT Analytics programs:")
	fmt.Println()
	for i, p := range programs {
		if p.IsMulti {
			labels := make([]string, 0, len(p.Streams))
			for j, s := range p.Streams {
				lbl := s.PLCLabel
				if lbl == "" {
					lbl = fmt.Sprintf("Stream %d", j+1)
				}
				labels = append(labels, lbl)
			}
			fmt.Printf("  %d. %-28s (%d streams: %s)\n",
				i+1, p.DirName, len(p.Streams), strings.Join(labels, " + "))
		} else {
			fmt.Printf("  %d. %-28s (%s)\n", i+1, p.DirName, p.Streams[0].BaseTopic)
		}
	}

	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("\nSelect program [1-%d]: ", len(programs))
	raw, _ := reader.ReadString('\n')
	sel, convErr := strconv.Atoi(strings.TrimSpace(raw))
	if convErr != nil || sel < 1 || sel > len(programs) {
		fmt.Fprintln(os.Stderr, "Invalid selection.")
		os.Exit(1)
	}
	prog := programs[sel-1]

	// Apply fixture-level capture_defaults for any flag not explicitly passed.
	fixtureYMLPath := filepath.Join(*fixturesDir, prog.DirName, "fixture.yml")
	if fm, fmErr := fixture.LoadFixtureMeta(fixtureYMLPath); fmErr == nil && fm.CaptureDefaults != nil {
		d := fm.CaptureDefaults
		var applied []string
		if !provided["timeout"] && d.Timeout != "" {
			if dur, parseErr := time.ParseDuration(d.Timeout); parseErr == nil {
				*timeout = dur
				applied = append(applied, fmt.Sprintf("timeout=%s", *timeout))
			}
		}
		if !provided["data-count"] && d.DataCount != 0 {
			*dataCount = d.DataCount
			applied = append(applied, fmt.Sprintf("data-count=%d", *dataCount))
		}
		if !provided["data-timeout"] && d.DataTimeout != "" {
			if dur, parseErr := time.ParseDuration(d.DataTimeout); parseErr == nil {
				*dataTimeout = dur
				applied = append(applied, fmt.Sprintf("data-timeout=%s", *dataTimeout))
			}
		}
		if len(applied) > 0 {
			fmt.Printf("\n  (fixture defaults applied: %s)\n", strings.Join(applied, ", "))
		}
	}

	// Apply --base-topic override for single-stream fixtures.
	if !prog.IsMulti && provided["base-topic"] && *baseTopic != "" {
		prog.Streams[0].BaseTopic = *baseTopic
	}

	var brokerURI string
	if *broker != "" {
		brokerURI = buildBrokerURI(*broker)
	} else {
		brokerURI = buildBrokerURI(*brokerAddr)
	}

	if prog.IsMulti {
		runMultiWizard(reader, prog, brokerURI, *fixturesDir, *timeout, *dataCount, *dataTimeout, *mqttClientID, *plcAddr)
	} else {
		runSingleWizard(reader, prog, brokerURI, *fixturesDir, *timeout, *dataCount, *dataTimeout, *mqttClientID, *plcAddr)
	}
}

// buildBrokerURI converts a listen address such as ":1883" into a full MQTT
// broker URI "tcp://127.0.0.1:1883".
func buildBrokerURI(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "tcp://127.0.0.1" + addr
	}
	return "tcp://" + addr
}

// --------------------------------------------------------------------------
// Program discovery
// --------------------------------------------------------------------------

// discoverPrograms walks fixturesDir one level deep, reads each fixture.yml,
// and returns program entries for fixtures that have a streams: list configured.
func discoverPrograms(fixturesDir string) ([]programEntry, error) {
	entries, err := os.ReadDir(fixturesDir)
	if err != nil {
		return nil, fmt.Errorf("read fixtures dir %s: %w", fixturesDir, err)
	}

	var programs []programEntry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ymlPath := filepath.Join(fixturesDir, e.Name(), "fixture.yml")
		fm, fmErr := fixture.LoadFixtureMeta(ymlPath)
		if fmErr != nil || len(fm.Streams) == 0 {
			continue // no fixture.yml or no streams configured — skip silently
		}

		streams := make([]streamEntry, len(fm.Streams))
		for i, sc := range fm.Streams {
			streams[i] = streamEntry{
				BaseTopic:   sc.BaseTopic,
				Sentinel:    sc.Sentinel,
				PLCLabel:    sc.PLCLabel,
				ProgramName: sc.Program,
			}
		}

		programs = append(programs, programEntry{
			DirName: e.Name(),
			DirPath: filepath.Join(fixturesDir, e.Name()),
			Streams: streams,
			IsMulti: len(streams) > 1,
		})
	}
	return programs, nil
}

// --------------------------------------------------------------------------
// Interactive helpers
// --------------------------------------------------------------------------

// pressEnter prints an optional prompt and then waits for the user to press
// Enter before returning.
func pressEnter(reader *bufio.Reader, prompt string) {
	if prompt != "" {
		fmt.Print(prompt)
	}
	fmt.Print("  Press Enter when done...")
	reader.ReadString('\n') //nolint:errcheck
}

// adsCheck attempts to verify via ADS that the correct PLC program is running
// by reading the sentinel symbol. If plcAddr or sentinel is empty the check is
// skipped silently. On failure, the user is warned but not blocked.
func adsCheck(reader *bufio.Reader, plcAddr, sentinel string) {
	if plcAddr == "" || sentinel == "" {
		return
	}
	fmt.Println("\nVerifying correct program is running via ADS...")

	// Construct the AMS NetID by appending ".1.1" to the IP address.
	netID := plcAddr + ".1.1"

	settings := ads.ClientSettings{
		TargetNetID: netID,
		RouterAddr:  plcAddr + ":48898",
		Timeout:     5 * time.Second,
	}
	settings.LoadDefaults()

	client := ads.NewClient(settings, slog.Default())
	if connErr := client.Connect(); connErr != nil {
		fmt.Printf("  ⚠️  ADS sentinel check failed: %v\n", connErr)
		fmt.Println("  This may mean the wrong PLC program is active, or ADS is not reachable.")
		fmt.Print("  Press Enter to continue anyway, or Ctrl+C to abort...")
		reader.ReadString('\n') //nolint:errcheck
		return
	}
	defer client.Disconnect() //nolint:errcheck

	// ADS port 851 = TwinCAT PLC Runtime 1
	if _, readErr := client.ReadValue(851, sentinel); readErr != nil {
		fmt.Printf("  ⚠️  ADS sentinel check failed: %v\n", readErr)
		fmt.Println("  This may mean the wrong PLC program is active, or ADS is not reachable.")
		fmt.Print("  Press Enter to continue anyway, or Ctrl+C to abort...")
		reader.ReadString('\n') //nolint:errcheck
		return
	}
	fmt.Println("  ✅  Sentinel OK — correct program confirmed.")
}

// --------------------------------------------------------------------------
// Single-stream wizard
// --------------------------------------------------------------------------

func runSingleWizard(
	reader *bufio.Reader,
	prog programEntry,
	brokerURI, fixturesDir string,
	timeout time.Duration,
	dataCount int,
	dataTimeout time.Duration,
	mqttClientID, plcAddr string,
) {
	stream := prog.Streams[0]
	fmt.Printf("\n=== Capture wizard: %s ===\n\n", prog.DirName)

	// Step 1 — Build
	fmt.Println("Step 1/3 — Build the project")
	fmt.Println("  In TwinCAT XAE:")
	fmt.Println("    • Open the project in TwinCAT XAE")
	fmt.Println("    • Build: Build > Build Solution (F7)")
	pressEnter(reader, "")

	// Step 2 — Activate Configuration (puts PLC straight into Run mode)
	fmt.Println("\nStep 2/3 — Activate Configuration")
	fmt.Println("  In TwinCAT XAE:")
	fmt.Println("    • TwinCAT > Activate Configuration")
	fmt.Println("    • Dialog 1: press OK to confirm the activation")
	fmt.Println("    • Dialog 2: press OK to start the project (Run mode)")
	fmt.Println("    • Wait for the TwinCAT icon in the system tray to turn green")
	pressEnter(reader, "")

	// Step 2.5 — ADS sentinel check (only when --plc-addr is provided)
	adsCheck(reader, plcAddr, stream.Sentinel)

	// Step 3 — Capture
	capturesDir := filepath.Join(fixturesDir, prog.DirName, "captures")
	fmt.Println("\nStep 3/3 — Capturing...")
	fmt.Printf("  Base topic:   %s\n", stream.BaseTopic)
	fmt.Printf("  Broker:       %s\n", brokerURI)
	fmt.Printf("  Captures dir: %s\n\n", capturesDir)
	fmt.Println("  Starting embedded MQTT broker capture — waiting for TwinCAT to publish symbols...")

	opts := capture.Options{
		BrokerURI:   brokerURI,
		ClientID:    mqttClientID + "-" + prog.DirName,
		SymbolTopic: stream.BaseTopic + "/Bin/Tx/Symbols",
		DescTopic:   stream.BaseTopic + "/Bin/Tx/Desc",
		DataTopic:   stream.BaseTopic + "/Bin/Tx/Data",
		BaseTopic:   stream.BaseTopic,
		Timeout:     timeout,
		OutDir:      capturesDir,
		DataCount:   dataCount,
		DataTimeout: dataTimeout,
	}
	result, runErr := capture.Run(context.Background(), opts)
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "ERROR: capture failed: %v\n", runErr)
		os.Exit(1)
	}

	fmt.Printf("\n✅  Captured %d symbols.\n", result.Fixture.NumSymbols)
	fmt.Printf("    Capture:      %s\n", result.CaptureDir)
	fmt.Printf("    Symbols YAML: %s\n", result.SymbolsYMLPath)
	if result.DescYMLPath != "" {
		fmt.Printf("    Desc YAML:    %s\n", result.DescYMLPath)
	}
	if result.DataDir != "" {
		fmt.Printf("    Data:         %s/ (%d messages)\n", result.DataDir, result.DataCount)
	}
}

// --------------------------------------------------------------------------
// Multi-stream wizard (e.g. InterleaveTest)
// --------------------------------------------------------------------------

func runMultiWizard(
	reader *bufio.Reader,
	prog programEntry,
	brokerURI, fixturesDir string,
	timeout time.Duration,
	dataCount int,
	dataTimeout time.Duration,
	mqttClientID, plcAddr string,
) {
	n := len(prog.Streams)
	fmt.Printf("\n=== Capture wizard: %s (%d streams) ===\n\n", prog.DirName, n)

	streamLabel := func(s streamEntry, idx int) string {
		if s.PLCLabel != "" {
			return s.PLCLabel
		}
		return fmt.Sprintf("Stream %d", idx+1)
	}
	prgName := func(s streamEntry) string {
		return s.ProgramName
	}

	// Step 1 — Build both projects
	fmt.Println("Step 1/3 — Build both projects")
	fmt.Println("  In TwinCAT XAE:")
	fmt.Println("    • Open BOTH TwinCAT projects (one per PLC)")
	fmt.Println("    • Build each: Build > Build Solution (F7)")
	pressEnter(reader, "")

	// Step 2 — Activate Configuration on each PLC (puts each into Run mode)
	fmt.Println("\nStep 2/3 — Activate Configuration on both PLCs")
	for i, s := range prog.Streams {
		fmt.Printf("  In TwinCAT XAE (%s — %s):\n", streamLabel(s, i), prgName(s))
		fmt.Println("    • TwinCAT > Activate Configuration")
		fmt.Println("    • Dialog 1: press OK to confirm the activation")
		fmt.Println("    • Dialog 2: press OK to start the project (Run mode)")
		fmt.Println("    • Wait for the green system tray icon")
	}
	pressEnter(reader, "")

	// Step 2.5 — ADS sentinel checks (one per stream if --plc-addr provided)
	if plcAddr != "" {
		for _, s := range prog.Streams {
			adsCheck(reader, plcAddr, s.Sentinel)
		}
	}

	// Step 3 — Concurrent capture
	capturesDir := filepath.Join(fixturesDir, prog.DirName, "captures")
	fmt.Printf("\nStep 3/3 — Capturing %d streams concurrently...\n", n)
	for i, s := range prog.Streams {
		fmt.Printf("  Stream %d (%s): %s\n", i+1, streamLabel(s, i), s.BaseTopic)
	}
	fmt.Printf("  Broker:       %s\n", brokerURI)
	fmt.Printf("  Captures dir: %s\n\n", capturesDir)

	type captureResult struct {
		result *capture.RunResult
		err    error
		stream streamEntry
		idx    int
	}
	results := make([]captureResult, n)

	var wg sync.WaitGroup
	var mu sync.Mutex
	for i, s := range prog.Streams {
		wg.Add(1)
		go func(idx int, stream streamEntry) {
			defer wg.Done()
			opts := capture.Options{
				BrokerURI:   brokerURI,
				ClientID:    fmt.Sprintf("%s-%s-%d", mqttClientID, prog.DirName, idx+1),
				SymbolTopic: stream.BaseTopic + "/Bin/Tx/Symbols",
				DescTopic:   stream.BaseTopic + "/Bin/Tx/Desc",
				DataTopic:   stream.BaseTopic + "/Bin/Tx/Data",
				BaseTopic:   stream.BaseTopic,
				Timeout:     timeout,
				OutDir:      capturesDir,
				DataCount:   dataCount,
				DataTimeout: dataTimeout,
			}
			res, runErr := capture.Run(context.Background(), opts)
			mu.Lock()
			results[idx] = captureResult{
				result: res,
				err:    runErr,
				stream: stream,
				idx:    idx,
			}
			mu.Unlock()
		}(i, s)
	}
	wg.Wait()

	// Check for any capture errors before writing the manifest.
	for _, r := range results {
		if r.err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: capture stream %d (%s) failed: %v\n",
				r.idx+1, streamLabel(r.stream, r.idx), r.err)
			os.Exit(1)
		}
	}

	// Write session manifest inside capturesDir.
	timestamp := time.Now().UTC().Format("20060102150405")
	sessionPath := filepath.Join(capturesDir, fmt.Sprintf("session-%s.yml", timestamp))

	streamRefs := make([]fixture.StreamRef, n)
	for i, r := range results {
		relPath, _ := filepath.Rel(capturesDir, r.result.SymbolsYMLPath)
		streamRefs[i] = fixture.StreamRef{
			BaseTopic:   r.stream.BaseTopic,
			PLCLabel:    r.stream.PLCLabel,
			FixtureFile: relPath,
		}
	}
	manifest := &fixture.SessionManifest{
		CapturedAt: time.Now().UTC().Format(time.RFC3339),
		ProgramDir: prog.DirName,
		Streams:    streamRefs,
	}
	if writeErr := fixture.WriteSession(sessionPath, manifest); writeErr != nil {
		fmt.Fprintf(os.Stderr, "WARNING: could not write session manifest: %v\n", writeErr)
	}

	fmt.Printf("\n✅  Captured %d streams.\n", n)
	for i, r := range results {
		fmt.Printf("    Stream %d (%s): %s\n",
			i+1, streamLabel(r.stream, i), r.result.SymbolsYMLPath)
	}
	fmt.Printf("    Session manifest: %s\n", sessionPath)
}
