GO ?= go
VERSION ?= dev
DIST_DIR ?= dist
LDFLAGS := -s -w -X main.version=$(VERSION)
GOFILES := $(shell find . -name '*.go' -not -path './$(DIST_DIR)/*')

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
	$(GO) build -ldflags "$(LDFLAGS)" -o tunnelflux ./cmd/tunnelflux

build-linux:
	mkdir -p $(DIST_DIR)/linux-amd64 $(DIST_DIR)/linux-arm64
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/linux-amd64/tunnelflux ./cmd/tunnelflux
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/linux-arm64/tunnelflux ./cmd/tunnelflux

build-darwin:
	mkdir -p $(DIST_DIR)/darwin-amd64 $(DIST_DIR)/darwin-arm64
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/darwin-amd64/tunnelflux ./cmd/tunnelflux
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/darwin-arm64/tunnelflux ./cmd/tunnelflux

dist: clean build-linux build-darwin
	mkdir -p $(DIST_DIR)/linux-amd64/configs $(DIST_DIR)/linux-arm64/configs $(DIST_DIR)/darwin-amd64/configs $(DIST_DIR)/darwin-arm64/configs
	cp README.md $(DIST_DIR)/linux-amd64/
	cp README.md $(DIST_DIR)/linux-arm64/
	cp README.md $(DIST_DIR)/darwin-amd64/
	cp README.md $(DIST_DIR)/darwin-arm64/
	cp configs/tunnelflux.example.json $(DIST_DIR)/linux-amd64/configs/
	cp configs/tunnelflux.example.json $(DIST_DIR)/linux-arm64/configs/
	cp configs/tunnelflux.example.json $(DIST_DIR)/darwin-amd64/configs/
	cp configs/tunnelflux.example.json $(DIST_DIR)/darwin-arm64/configs/
	install -m 0755 install.sh $(DIST_DIR)/linux-amd64/install.sh
	install -m 0755 install.sh $(DIST_DIR)/linux-arm64/install.sh
	install -m 0755 install.sh $(DIST_DIR)/darwin-amd64/install.sh
	install -m 0755 install.sh $(DIST_DIR)/darwin-arm64/install.sh
	tar -C $(DIST_DIR)/linux-amd64 -czf $(DIST_DIR)/tunnelflux-linux-amd64.tar.gz .
	tar -C $(DIST_DIR)/linux-arm64 -czf $(DIST_DIR)/tunnelflux-linux-arm64.tar.gz .
	tar -C $(DIST_DIR)/darwin-amd64 -czf $(DIST_DIR)/tunnelflux-darwin-amd64.tar.gz .
	tar -C $(DIST_DIR)/darwin-arm64 -czf $(DIST_DIR)/tunnelflux-darwin-arm64.tar.gz .
	install -m 0755 install.sh $(DIST_DIR)/install.sh
	cd $(DIST_DIR) && if command -v sha256sum >/dev/null 2>&1; then sha256sum tunnelflux-linux-amd64.tar.gz tunnelflux-linux-arm64.tar.gz tunnelflux-darwin-amd64.tar.gz tunnelflux-darwin-arm64.tar.gz; else shasum -a 256 tunnelflux-linux-amd64.tar.gz tunnelflux-linux-arm64.tar.gz tunnelflux-darwin-amd64.tar.gz tunnelflux-darwin-arm64.tar.gz; fi > checksums.txt

clean:
	rm -rf $(DIST_DIR) tunnelflux
