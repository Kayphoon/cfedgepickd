GO ?= go
VERSION ?= dev
DIST_DIR ?= dist
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: fmt test build build-linux dist clean

fmt:
	$(GO)fmt -w $$(find . -name '*.go')

test:
	$(GO) test ./...

build:
	$(GO) build -ldflags "$(LDFLAGS)" -o cfpick ./cmd/cfpick
	$(GO) build -ldflags "$(LDFLAGS)" -o cfedgepickd ./cmd/cfedgepickd
	$(GO) build -ldflags "$(LDFLAGS)" -o cfedgepickctl ./cmd/cfedgepickctl

build-linux:
	mkdir -p $(DIST_DIR)/linux-amd64 $(DIST_DIR)/linux-arm64
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/linux-amd64/cfpick ./cmd/cfpick
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/linux-amd64/cfedgepickd ./cmd/cfedgepickd
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/linux-amd64/cfedgepickctl ./cmd/cfedgepickctl
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/linux-arm64/cfpick ./cmd/cfpick
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/linux-arm64/cfedgepickd ./cmd/cfedgepickd
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/linux-arm64/cfedgepickctl ./cmd/cfedgepickctl

dist: build-linux
	cp README.md deploy/install.sh deploy/cfpick.service configs/cfpick.example.json $(DIST_DIR)/linux-amd64/
	cp README.md deploy/install.sh deploy/cfpick.service configs/cfpick.example.json $(DIST_DIR)/linux-arm64/
	tar -C $(DIST_DIR)/linux-amd64 -czf $(DIST_DIR)/cfpick-linux-amd64.tar.gz .
	tar -C $(DIST_DIR)/linux-arm64 -czf $(DIST_DIR)/cfpick-linux-arm64.tar.gz .
	tar -C $(DIST_DIR)/linux-amd64 -czf $(DIST_DIR)/cfedgepickd-linux-amd64.tar.gz .
	tar -C $(DIST_DIR)/linux-arm64 -czf $(DIST_DIR)/cfedgepickd-linux-arm64.tar.gz .

clean:
	rm -rf $(DIST_DIR) cfpick cfedgepickd cfedgepickctl
