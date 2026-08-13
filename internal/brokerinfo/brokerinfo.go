// Package brokerinfo probes an MQTT broker's $SYS topics to identify the
// broker family, version, and build timestamp. This metadata is stored in
// test fixture YAML files to help diagnose parser bugs that are
// broker-specific.
//
// # Supported Brokers
//
// Not all brokers publish $SYS topics. The table below summarises current
// support status. See docs/BROKERS.md for the full reference.
//
//   - mosquitto:        ✅  Fully implemented. Tested against Mosquitto 2.x.
//   - vernemq:          ⚠️  Implemented; verify on a real VerneMQ instance.
//   - hivemq:           ⚠️  Implemented; verify on a real HiveMQ instance.
//   - flashmq:          ⚠️  Implemented; verify on a real FlashMQ instance.
//   - emqx:             ⚠️  Implemented; verify exact topic/payload format on EMQX.
//   - nanomq:           ⚠️  Implemented; verify $SYS/brokers/nano/version path.
//   - rmqtt:            ⚠️  Partial — fingerprints by JSON payload shape; no version string.
//     TODO: fetch version via HTTP API GET /api/v1/brokers.
//   - rabbitmq:         ❌  No $SYS support. Uses Prometheus + Management REST API.
//     TODO: probe via --broker-mgmt-url flag if added.
//   - aws-iot-core:     ❌  No $SYS support. Uses CloudWatch + Device Defender.
//   - azure-iot-hub:    ❌  No $SYS support. Uses Azure Monitor.
//   - azure-iot-ops:    ❌  No $SYS support (full MQTT 5.0 Kubernetes broker, GA 2024).
//   - google-iot-core:  ❌  Shut down August 16, 2023.
//   - flespi:           ❌  No $SYS support. Uses HTTP REST API at flespi.io.
//   - solace-pubsub+:   ❌  No $SYS support. Uses Solace Event Portal / stats API.
//   - kafka-mqtt:       ❌  Not a native MQTT broker; $SYS not applicable.
package brokerinfo

import (
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// BrokerInfo holds identification data retrieved from the broker's $SYS
// topics. Fields may be empty if the broker does not support $SYS or if the
// probe timed out before a response arrived.
type BrokerInfo struct {
	// BrokerType is the detected broker family (e.g. "mosquitto", "emqx").
	// Empty when detection failed or the broker does not publish $SYS topics.
	BrokerType string

	// Version is the raw version string as reported by $SYS.
	// Example: "mosquitto version 2.0.18"
	// For RMQTT this will be a descriptive placeholder — version must be
	// fetched separately via the HTTP API.
	Version string

	// BuildTimestamp is the broker build date/time string, if available.
	// Currently only populated for Mosquitto via $SYS/broker/timestamp.
	BuildTimestamp string
}

// Probe subscribes to known $SYS topics and returns the first BrokerInfo
// detected within timeout. It is best-effort: an empty BrokerInfo is returned
// when no $SYS response arrives in time (expected for cloud brokers such as
// AWS IoT Core and Azure IoT Hub, or when broker ACLs block $SYS access).
//
// Probe always returns before (timeout + 500 ms enrichment window) elapses,
// so it is safe to call in a fire-and-forget goroutine alongside the main
// capture flow.
func Probe(client mqtt.Client, timeout time.Duration) BrokerInfo {
	versionCh := make(chan BrokerInfo, 1)
	timestampCh := make(chan string, 1)

	var versionOnce, timestampOnce sync.Once

	deliverVersion := func(info BrokerInfo) {
		versionOnce.Do(func() { versionCh <- info })
	}

	// ------------------------------------------------------------------
	// $SYS/broker/version — Mosquitto-compatible brokers
	//
	// Covers: Mosquitto, Cedalo Pro Mosquitto, VerneMQ, HiveMQ CE/Enterprise,
	// FlashMQ. All use the singular "broker" path prefix.
	//
	// Mosquitto delivers this once immediately on subscribe (not retained).
	// ACL note: Mosquitto 2.0+ restricts $SYS reads to localhost by default.
	// ------------------------------------------------------------------
	client.Subscribe("$SYS/broker/version", 0, func(_ mqtt.Client, msg mqtt.Message) {
		payload := string(msg.Payload())
		deliverVersion(BrokerInfo{
			BrokerType: detectMosquittoCompatible(payload),
			Version:    payload,
		})
	})

	// ------------------------------------------------------------------
	// $SYS/broker/timestamp — Mosquitto build date (optional enrichment).
	//
	// Only Mosquitto (and Cedalo Pro Mosquitto) are known to publish this
	// topic. Example payload: "2023-08-01 12:00:00+0000".
	// ------------------------------------------------------------------
	client.Subscribe("$SYS/broker/timestamp", 0, func(_ mqtt.Client, msg mqtt.Message) {
		timestampOnce.Do(func() { timestampCh <- string(msg.Payload()) })
	})

	// ------------------------------------------------------------------
	// $SYS/brokers/# — Node-scoped brokers (plural path prefix)
	//
	// Covers: EMQX, NanoMQ, RMQTT — and possibly VerneMQ alternate paths.
	//
	// These brokers embed a node identifier in the topic:
	//   EMQX:   $SYS/brokers/{emqx@hostname}/version
	//   NanoMQ: $SYS/brokers/nano/version
	//   RMQTT:  $SYS/brokers/{numericID}/stats  (JSON, no version topic)
	//
	// Note: subscribing to $SYS/brokers/# will NOT match $SYS/broker/version
	// (singular "broker" vs plural "brokers" — these are distinct trees).
	// ------------------------------------------------------------------
	client.Subscribe("$SYS/brokers/#", 0, func(_ mqtt.Client, msg mqtt.Message) {
		topic := msg.Topic()
		payload := string(msg.Payload())
		if bt, ver := detectNodeScoped(topic, payload); ver != "" {
			deliverVersion(BrokerInfo{BrokerType: bt, Version: ver})
		}
	})

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	var result BrokerInfo

	select {
	case result = <-versionCh:
		// Version detected. For brokers that also publish a build timestamp,
		// wait a short window to enrich the result before returning.
		if result.BrokerType == "mosquitto" || result.BrokerType == "mosquitto-compatible" {
			enrichTimer := time.NewTimer(500 * time.Millisecond)
			defer enrichTimer.Stop()
			select {
			case ts := <-timestampCh:
				result.BuildTimestamp = ts
			case <-enrichTimer.C:
				// No timestamp within enrichment window — proceed without it.
			case <-deadline.C:
				// Overall timeout hit during enrichment window.
			}
		}
	case <-deadline.C:
		// No $SYS response within timeout. Return empty BrokerInfo.
		// This is expected for cloud-managed brokers or when broker ACL
		// blocks $SYS access from non-localhost clients.
	}

	client.Unsubscribe("$SYS/broker/version", "$SYS/broker/timestamp", "$SYS/brokers/#")
	return result
}

// detectMosquittoCompatible inspects a version payload received on
// $SYS/broker/version and returns the broker type name. Mosquitto and
// compatible brokers all publish to the singular $SYS/broker/version path,
// so we distinguish them by the payload content.
func detectMosquittoCompatible(payload string) string {
	lower := strings.ToLower(payload)
	switch {
	case strings.Contains(lower, "mosquitto"):
		// Covers Eclipse Mosquitto and Cedalo Pro Mosquitto — both produce a
		// version string starting with "mosquitto". Distinguish between them
		// post-match by checking for "cedalo" in the payload if needed.
		return "mosquitto"
	case strings.Contains(lower, "vernemq"):
		return "vernemq"
	case strings.Contains(lower, "hivemq"):
		return "hivemq"
	case strings.Contains(lower, "flashmq"):
		return "flashmq"
	default:
		// Something responded to $SYS/broker/version but we do not recognise
		// its version string. Treat as Mosquitto-compatible and preserve the
		// raw payload for inspection.
		return "mosquitto-compatible"
	}
}

// detectNodeScoped inspects a topic and payload from $SYS/brokers/# and
// returns (brokerType, versionString). Returns ("", "") if no match.
func detectNodeScoped(topic, payload string) (string, string) {
	// ------------------------------------------------------------------
	// EMQX: $SYS/brokers/{emqx@hostname}/version
	// Node names contain "@" — e.g. "emqx@node-hostname".
	// TODO: Verify exact payload format for EMQX $SYS version topic on a
	//       real EMQX instance (both OSS and Enterprise editions).
	// ------------------------------------------------------------------
	if strings.HasSuffix(topic, "/version") && strings.Contains(topic, "@") {
		return "emqx", payload
	}

	// ------------------------------------------------------------------
	// NanoMQ: $SYS/brokers/nano/version
	// Fixed "nano" node name used by EMQ's edge broker.
	// TODO: Confirm this is the canonical path on NanoMQ >= 0.18.
	// ------------------------------------------------------------------
	if strings.Contains(topic, "/nano/") && strings.HasSuffix(topic, "/version") {
		return "nanomq", payload
	}

	// ------------------------------------------------------------------
	// RMQTT: no dedicated version topic.
	// Stats:   $SYS/brokers/{numericID}/stats   — JSON
	// Metrics: $SYS/brokers/{numericID}/metrics — JSON
	//
	// Fingerprint by characteristic JSON keys present in the stats payload.
	// Version must be fetched separately via HTTP API GET /api/v1/brokers.
	// TODO: Optionally fetch version if a --broker-mgmt-url flag is added.
	// See: https://github.com/rmqtt/rmqtt
	// ------------------------------------------------------------------
	if strings.Contains(payload, `"connections.count"`) ||
		strings.Contains(payload, `"handshakings.count"`) {
		return "rmqtt", "rmqtt (version unavailable via $SYS; query HTTP API GET /api/v1/brokers)"
	}

	// ------------------------------------------------------------------
	// Generic node-scoped fallback: a /version topic arrived but we did not
	// recognise the node name format. Preserve the payload for inspection.
	// ------------------------------------------------------------------
	if strings.HasSuffix(topic, "/version") {
		return "unknown-node-scoped", payload
	}

	return "", ""
}
