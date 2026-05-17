GO ?= go
VERSION ?= dev
DIST_DIR ?= dist
LDFLAGS := -s -w -X main.version=$(VERSION)
GOFILES := $(shell find . -name '*.go' -not -path './$(DIST_DIR)/*')
DIST_ASSETS := README.md deploy/cfpick.service deploy/cfedgepickd.service configs/cfpick.example.json configs/cfedgepickd.example.json

.PHONY: fmt fmt-check test build build-linux build-darwin dist clean

fmt:
	$(GO)fmt -w $(GOFILES)

fmt-check:
	@unformatted="$$($(GO)fmt -l $(GOFILES))"; status="$$?"; \
	if [ "$$status" -ne 0 ]; then \
		exit "$$status"; \
	fi; \
	if [ -n "$$unformatted" ]; then \
		echo "$$unformatted"; \
		exit 1; \
	fi

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

build-darwin:
	mkdir -p $(DIST_DIR)/darwin-amd64 $(DIST_DIR)/darwin-arm64
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/darwin-amd64/cfpick ./cmd/cfpick
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/darwin-amd64/cfedgepickd ./cmd/cfedgepickd
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/darwin-amd64/cfedgepickctl ./cmd/cfedgepickctl
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/darwin-arm64/cfpick ./cmd/cfpick
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/darwin-arm64/cfedgepickd ./cmd/cfedgepickd
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/darwin-arm64/cfedgepickctl ./cmd/cfedgepickctl

dist: clean build-linux build-darwin
	cp $(DIST_ASSETS) $(DIST_DIR)/linux-amd64/
	cp $(DIST_ASSETS) $(DIST_DIR)/linux-arm64/
	cp $(DIST_ASSETS) $(DIST_DIR)/darwin-amd64/
	cp $(DIST_ASSETS) $(DIST_DIR)/darwin-arm64/
	install -m 0755 install.sh $(DIST_DIR)/linux-amd64/install.sh
	install -m 0755 install.sh $(DIST_DIR)/linux-arm64/install.sh
	install -m 0755 install.sh $(DIST_DIR)/darwin-amd64/install.sh
	install -m 0755 install.sh $(DIST_DIR)/darwin-arm64/install.sh
	tar -C $(DIST_DIR)/linux-amd64 -czf $(DIST_DIR)/cfpick-linux-amd64.tar.gz .
	tar -C $(DIST_DIR)/linux-arm64 -czf $(DIST_DIR)/cfpick-linux-arm64.tar.gz .
	tar -C $(DIST_DIR)/darwin-amd64 -czf $(DIST_DIR)/cfpick-darwin-amd64.tar.gz .
	tar -C $(DIST_DIR)/darwin-arm64 -czf $(DIST_DIR)/cfpick-darwin-arm64.tar.gz .
	install -m 0755 install.sh $(DIST_DIR)/install.sh
	cd $(DIST_DIR) && if command -v sha256sum >/dev/null 2>&1; then sha256sum cfpick-linux-amd64.tar.gz cfpick-linux-arm64.tar.gz cfpick-darwin-amd64.tar.gz cfpick-darwin-arm64.tar.gz; else shasum -a 256 cfpick-linux-amd64.tar.gz cfpick-linux-arm64.tar.gz cfpick-darwin-amd64.tar.gz cfpick-darwin-arm64.tar.gz; fi > checksums.txt

clean:
	rm -rf $(DIST_DIR) cfpick cfedgepickd cfedgepickctl
