MODULE := github.com/siyka-au/twincat-analytics-go

BIN_DIR   := bin
BINARIES  := tcaly tcaly-mqtt capture runner service

GO        := go
GOFLAGS   :=
LDFLAGS   := -s -w

.PHONY: all clean test vet fmt $(BINARIES)

all: $(BINARIES)

tcaly:
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/tcaly ./cmd/tcaly

tcaly-mqtt:
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/tcaly-mqtt ./cmd/tcaly-mqtt

capture:
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/capture ./cmd/capture

runner:
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/runner ./cmd/runner

service:
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/service ./cmd/service

$(BIN_DIR):
	mkdir -p $(BIN_DIR)

$(BINARIES): | $(BIN_DIR)

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

clean:
	rm -rf $(BIN_DIR)
