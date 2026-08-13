package config

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	MQTTURI      string
	MQTTUser     string
	MQTTPass     string
	MQTTClientID string
	TopicBase    string
	SymbolTopic  string
	DescTopic    string
	DataTopic    string
}

func Load() Config {
	// 1. Try to load .env file (no error if it doesn't exist)
	_ = godotenv.Load()

	// 2. Define Command Line Flags
	// These use the TC_ANALYTICS_ prefixed Env Vars as defaults
	fURI := flag.String("mqtt-uri", getEnv("TC_ANALYTICS_MQTT_URI", "tcp://127.0.0.1:1883"), "MQTT Broker URI")
	fUser := flag.String("mqtt-user", getEnv("TC_ANALYTICS_MQTT_USER", ""), "MQTT Username")
	fPass := flag.String("mqtt-pass", getEnv("TC_ANALYTICS_MQTT_PASS", ""), "MQTT Password")
	fBase := flag.String("base-topic", getEnv("TC_ANALYTICS_TOPIC_BASE", ""), "TwinCAT Base Topic (Required)")
	fClientID := flag.String("client-id", getEnv("TC_ANALYTICS_MQTT_CLIENT_ID", ""), "MQTT Client ID")

	flag.Parse()

	// 3. Validation: Topic Base is REQUIRED
	if *fBase == "" {
		fmt.Println("❌ Error: --base-topic or TC_ANALYTICS_TOPIC_BASE must be set.")
		flag.Usage()
		os.Exit(1)
	}

	// 4. Generate Client ID if not specified (Timestamp to 1-second accuracy)
	finalClientID := *fClientID
	if finalClientID == "" {
		finalClientID = fmt.Sprintf("twincat-analytics-parser-go-%d", time.Now().Unix())
	}

	return Config{
		MQTTURI:      *fURI,
		MQTTUser:     *fUser,
		MQTTPass:     *fPass,
		MQTTClientID: finalClientID,
		TopicBase:    *fBase,
		SymbolTopic:  fmt.Sprintf("%s/Bin/Tx/Symbols", *fBase),
		DescTopic:    fmt.Sprintf("%s/Bin/Tx/Desc", *fBase),
		DataTopic:    fmt.Sprintf("%s/Bin/Tx/Data", *fBase),
	}
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}
