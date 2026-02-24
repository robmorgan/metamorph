BINARY   := metamorph
WEB_DIR  := web
WEB_OUT  := $(WEB_DIR)/out

.PHONY: all build build-web build-go build-noui clean

# Default target: build everything
all: build

# Full build: web UI → Go binary
build: build-web build-go

# Build the Next.js web UI
build-web: $(WEB_DIR)/node_modules
	npm --prefix $(WEB_DIR) run build

# Install Node dependencies if needed
$(WEB_DIR)/node_modules: $(WEB_DIR)/package.json
	npm --prefix $(WEB_DIR) install
	@touch $@

# Build the Go binary (embeds web/out/ via //go:embed)
build-go: $(WEB_OUT)
	go build -o $(BINARY) .

# Build Go binary without the web UI
build-noui:
	go build -tags noui -o $(BINARY) .

# Remove build artifacts
clean:
	rm -rf $(BINARY) $(WEB_OUT)
