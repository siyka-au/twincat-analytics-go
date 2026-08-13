package main

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/joho/godotenv"
	"github.com/siyka-au/twincat-analytics-go/layout"
	"github.com/siyka-au/twincat-analytics-go/parser"
	"github.com/spf13/cobra"
)

var logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "tcaly-mqtt",
	Short: "Subscribe to a TwinCAT Analytics MQTT stream and decode data messages",
	Long: `tcaly-mqtt connects to an MQTT broker, subscribes to the TwinCAT Analytics
Bin/Tx/Symbols and Bin/Tx/Data topics, and logs decoded field values to stdout.

Connection settings are resolved in priority order:
  1. CLI flags
  2. Environment variables (TC_ANALYTICS_*)
  3. .env file in the current directory`,
	RunE: runMQTT,
}

func init() {
	_ = godotenv.Load()

	rootCmd.Flags().String("mqtt-uri", getEnv("TC_ANALYTICS_MQTT_URI", "tcp://127.0.0.1:1883"), "MQTT broker URI (tcp://, ssl://, ws://, wss://)")
	rootCmd.Flags().String("mqtt-user", getEnv("TC_ANALYTICS_MQTT_USER", ""), "MQTT username")
	rootCmd.Flags().String("mqtt-pass", getEnv("TC_ANALYTICS_MQTT_PASS", ""), "MQTT password")
	rootCmd.Flags().String("base-topic", getEnv("TC_ANALYTICS_TOPIC_BASE", ""), "TwinCAT base topic — required (e.g. Devices/MyPLC)")
	rootCmd.Flags().String("client-id", getEnv("TC_ANALYTICS_MQTT_CLIENT_ID", ""), "MQTT client ID (auto-generated if empty)")
	_ = rootCmd.MarkFlagRequired("base-topic")
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func runMQTT(cmd *cobra.Command, args []string) error {
	mqttURI, _ := cmd.Flags().GetString("mqtt-uri")
	mqttUser, _ := cmd.Flags().GetString("mqtt-user")
	mqttPass, _ := cmd.Flags().GetString("mqtt-pass")
	baseTopic, _ := cmd.Flags().GetString("base-topic")
	clientID, _ := cmd.Flags().GetString("client-id")

	if clientID == "" {
		clientID = fmt.Sprintf("tcaly-mqtt-%d", time.Now().Unix())
	}

	symbolTopic := baseTopic + "/Bin/Tx/Symbols"
	dataTopic := baseTopic + "/Bin/Tx/Data"

	registry := layout.NewRegistry()
	queue := layout.NewPendingQueue(256)

	handleSymbols := func(_ mqtt.Client, msg mqtt.Message) {
		s, err := parser.ParseSymbolStream(msg.Payload())
		if err != nil {
			logger.Error("Symbols parse failed", "error", err)
			return
		}
		l := layout.NewLayoutFromStream(s)
		registry.Register(l)
		logger.Info("Layout registered",
			"guid", l.GUID,
			"fields", len(l.Fields),
			"sample_bytes", l.SampleDataSize,
		)
		for _, payload := range queue.Drain(l.GUID) {
			processData(l, payload)
		}
	}

	handleData := func(_ mqtt.Client, msg mqtt.Message) {
		payload := msg.Payload()
		dm, err := layout.ParseDataMessage(payload)
		if err != nil {
			logger.Warn("Data message parse failed", "error", err)
			return
		}
		l, ok := registry.Get(dm.Header.LayoutGUID)
		if !ok {
			dropped := queue.Enqueue(dm.Header.LayoutGUID, payload)
			if dropped {
				logger.Warn("Data queue full, oldest payload dropped", "guid", dm.Header.LayoutGUID)
			}
			return
		}
		processData(l, payload)
	}

	opts := mqtt.NewClientOptions().
		AddBroker(mqttURI).
		SetClientID(clientID).
		SetAutoReconnect(false)

	if mqttUser != "" {
		opts.SetUsername(mqttUser).SetPassword(mqttPass)
	}

	opts.OnConnect = func(client mqtt.Client) {
		logger.Info("MQTT connected", "uri", mqttURI, "client_id", clientID)
		client.Subscribe(symbolTopic, 1, handleSymbols)
		client.Subscribe(dataTopic, 0, handleData)
		logger.Info("Subscribed", "symbols", symbolTopic, "data", dataTopic)
	}

	opts.OnConnectionLost = func(client mqtt.Client, err error) {
		logger.Error("Connection lost", "error", err)
		go backoff(client)
	}

	client := mqtt.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		logger.Warn("Initial connect failed, entering backoff", "error", token.Error())
		go backoff(client)
	}

	select {}
}

func processData(l *layout.Layout, payload []byte) {
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
		for _, fv := range l.ParseSample(sample.Raw) {
			logger.Info("sample",
				"sample_idx", i,
				"ts", ts,
				"name", fv.Field.Name,
				"value", fv.Value,
			)
		}
	}
}

func backoff(client mqtt.Client) {
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
		logger.Warn("Reconnecting...", "next_attempt_in", delay)
	}
}
