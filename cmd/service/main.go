package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/siyka-au/twincat-analytics-go/internal/config"
	"github.com/siyka-au/twincat-analytics-go/layout"
	"github.com/siyka-au/twincat-analytics-go/parser"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	metricapi "go.opentelemetry.io/otel/metric"
	metricsdk "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
)

var (
	logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// layoutRegistry maps layout GUIDs to their parsed Layout descriptors.
	// Populated when a Bin/Tx/Symbols message is received.
	layoutRegistry = layout.NewRegistry()

	// dataQueue buffers Bin/Tx/Data payloads whose layout GUID is not yet
	// known.  Payloads are replayed once the matching Symbols arrives.
	// The cap of 256 per GUID prevents unbounded growth during startup races.
	dataQueue = layout.NewPendingQueue(256)

	// OTel Metrics
	meter       metricapi.Meter
	msgCounter  metricapi.Int64Counter
	payloadSize metricapi.Int64Histogram
)

func main() {
	cfg := config.Load()
	initMetrics(cfg)

	opts := mqtt.NewClientOptions().
		AddBroker(cfg.MQTTURI).
		SetClientID(cfg.MQTTClientID).
		SetAutoReconnect(false)

	if cfg.MQTTUser != "" {
		opts.SetUsername(cfg.MQTTUser).SetPassword(cfg.MQTTPass)
	}

	opts.OnConnect = func(client mqtt.Client) {
		logger.Info("✅ MQTT Connected", "uri", cfg.MQTTURI, "id", cfg.MQTTClientID)
		client.Subscribe(cfg.SymbolTopic, 1, handleSymbolStream)
		client.Subscribe(cfg.DataTopic, 0, handleDataStream)
	}

	opts.OnConnectionLost = func(client mqtt.Client, err error) {
		logger.Error("❌ Connection lost", "error", err)
		go performBackoff(client)
	}

	client := mqtt.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		go performBackoff(client)
	}

	select {}
}

func handleSymbolStream(client mqtt.Client, msg mqtt.Message) {
	s, err := parser.ParseSymbolStream(msg.Payload())
	if err != nil {
		logger.Error("Symbols parse failed", "error", err)
		return
	}

	l := layout.NewLayoutFromStream(s)

	layoutRegistry.Register(l)
	logger.Info("Layout registered",
		"guid", l.GUID,
		"symbols", len(l.Fields),
		"sample_bytes", l.SampleDataSize,
	)

	// Replay any data messages that arrived before this layout was known.
	queued := dataQueue.Drain(l.GUID)
	if len(queued) > 0 {
		logger.Info("Replaying queued data messages", "guid", l.GUID, "count", len(queued))
		for _, payload := range queued {
			processMQTTDataPayload(l, payload)
		}
	}
}

func handleDataStream(client mqtt.Client, msg mqtt.Message) {
	payload := msg.Payload()
	ctx := context.Background()

	if msgCounter != nil {
		msgCounter.Add(ctx, 1, metricapi.WithAttributes(attribute.String("topic", msg.Topic())))
	}
	if payloadSize != nil {
		payloadSize.Record(ctx, int64(len(payload)))
	}

	// Parse the data message framing to extract the layout GUID.
	dm, err := layout.ParseDataMessage(payload)
	if err != nil {
		logger.Warn("Data message parse failed", "error", err)
		return
	}

	guid := dm.Header.LayoutGUID
	l, ok := layoutRegistry.Get(guid)
	if !ok {
		// Layout not yet known — buffer for later.
		dropped := dataQueue.Enqueue(guid, payload)
		if dropped {
			logger.Warn("Data queue full, oldest payload dropped",
				"guid", guid,
				"queued", dataQueue.Len(),
			)
		} else {
			logger.Debug("Data message queued (layout not yet known)",
				"guid", guid,
				"queued", dataQueue.Len(),
			)
		}
		return
	}

	processMQTTDataPayload(l, payload)
}

// processMQTTDataPayload parses a raw Bin/Tx/Data payload using the given
// Layout and logs each decoded field value.  Extend this function to forward
// values to OTel, a time-series DB, or any downstream sink.
func processMQTTDataPayload(l *layout.Layout, payload []byte) {
	dm, err := layout.ParseDataMessage(payload)
	if err != nil {
		logger.Error("Data message re-parse failed", "error", err)
		return
	}

	for i, sample := range dm.Samples {
		ts := "—"
		if sample.Timestamp != nil {
			ts = sample.Timestamp.Format("15:04:05.000000000")
		}

		values := l.ParseSample(sample.Raw)
		for _, fv := range values {
			logger.Debug("sample",
				"sample_idx", i,
				"ts", ts,
				"name", fv.Field.Name,
				"type", fv.Field.TypeName,
				"value", fv.Value,
			)
		}
	}
}

func initMetrics(cfg config.Config) {
	ctx := context.Background()

	// Create resource (naming the service)
	res, _ := resource.New(ctx, resource.WithAttributes(semconv.ServiceNameKey.String("twincat-parser")))

	// Exporter 1: "Nasty" stdout (Pretty JSON)
	consoleExporter, _ := stdoutmetric.New(stdoutmetric.WithPrettyPrint())

	options := []metricsdk.Option{
		metricsdk.WithResource(res),
		metricsdk.WithReader(metricsdk.NewPeriodicReader(consoleExporter, metricsdk.WithInterval(30*time.Second))),
	}

	// Exporter 2: Optional OTLP (SigNoz)
	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); endpoint != "" {
		otlpExporter, err := otlpmetrichttp.New(ctx)
		if err == nil {
			options = append(options, metricsdk.WithReader(metricsdk.NewPeriodicReader(otlpExporter, metricsdk.WithInterval(10*time.Second))))
			logger.Info("📡 OTLP Enabled", "target", endpoint)
		}
	}

	provider := metricsdk.NewMeterProvider(options...)
	otel.SetMeterProvider(provider)

	meter = otel.Meter("twincat-parser")
	msgCounter, _ = meter.Int64Counter("twincat_messages_total")
	payloadSize, _ = meter.Int64Histogram("twincat_payload_bytes")
}

func performBackoff(client mqtt.Client) {
	delay := 1 * time.Second
	for {
		time.Sleep(delay)
		if token := client.Connect(); token.Wait() && token.Error() == nil {
			return
		}
		delay *= 2
		if delay > 60*time.Second {
			delay = 60 * time.Second
		}
		logger.Warn("Reconnecting...", "wait", delay)
	}
}
