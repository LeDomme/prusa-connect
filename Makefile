# Run `make` to see the list of available commands.

.PHONY: help run demo-modules demo install-deps install-hooks generate-printer-config certificate-server certificate-prusa certificate-all server-up server-down server-restart status logs build sync-version build-config _build-full-binary build-full build-ci build-minimal _printer-build-inputs build-printer build-cross _build-cross-full _build-cross-minimal print-build-tags compose-build compose-check _ensure-pnpm clean clean-js clean-frontend clean-docs test test-unit test-race test-race-linux test-engines test-engines-down module-database-e2e upgrade-process-e2e test-adapters fixtures-up fixtures-down postgres-dev-up postgres-dev-down postgres-dev-reset test-matrix test-matrix-full test-e2e test-firmware mini404-smoke firmware-up test-printer test-minimal test-config test-full coverage coverage-integration journey-check oidc-providers-check preflight test-shard go-cache-warm ci-local ci-local-fast ci-local-list pre-commit lint lint-fix style-check arch-check workflows-check secrets-check makefile-check changelog-check changelog-new changelog-preview commit-style-check adr-new migration-new hooks-check go-version-check vuln-check binary-check deadcode-check sdk-check side-modules-check api-check api-accept vocab-check vocab-accept generate-error-codes error-codes-check generate-extensions extensions-check generate-capabilities capabilities-check generate-resource-kinds resource-kinds-check generate-authz-vocabulary authz-vocabulary-check generate-event-types event-types-check generate-slots slots-check generate-device-issues device-issues-check generate-device-models device-models-check generate-printer-error-codes generate-wire-protocol wire-check generate-openapi openapi-check docs install-docs-deps docs-site-check docs-links-check docs-tables-check docs-check generate-config-reference config-reference-check generate-sdk-reference sdk-reference-check docs-up docs-down _client-sdk-deps client-sdk-build generate-client-sdk client-sdk-check client-sdk-install-check fractal-client-build _client-ui-deps client-ui-build client-ui-lint client-ui-svelte-check client-ui-test client-ui-storybook client-ui-check _app-deps frontend-build frontend-storybook-deps frontend-storybook design-check fleet-stats-ui-build frontend-check embed-app embedded-app-check clean-app registry-up registry-down registry-reset registry-publish registry-status registry-use scaffold-driver new-module example-spools-check docker-check docker-clean mockup-up mockup-status mockup-seed mockup-check mockup-down release-alpha release-beta release-stable

CONFIG_FILE ?= config/config.yaml
EXTRA_BUILD_TAGS ?=

# On Windows, run make from Git Bash. It does not work from PowerShell or cmd.
ifeq ($(shell sh -c "echo ok"),)
$(error This Makefile needs a POSIX shell and found no sh on PATH. On Windows, run make from Git Bash, not from PowerShell or cmd)
endif

# go build accepts any word as a tag, so a misspelt extra tag would build a
# binary without the part it names. cmd/detect-build-tags refuses such a word,
# and it is asked here once, so every target stops before it runs. The image
# build asks the same command.
ifneq ($(strip $(EXTRA_BUILD_TAGS)),)
EXTRA_BUILD_TAGS_REFUSAL := $(shell go run ./cmd/detect-build-tags --config "$(CONFIG_FILE)" --extra "$(EXTRA_BUILD_TAGS)" 2>&1 >/dev/null)
ifneq ($(EXTRA_BUILD_TAGS_REFUSAL),)
$(error $(EXTRA_BUILD_TAGS_REFUSAL))
endif
endif

# These two ports come from the configuration file. The awk command has to
# stay on one line, because make breaks it when it is split with a backslash.
SERVER_PORT := $(shell awk '/^core:/ { c=1; next } c && /^  api:/ { a=1; next } a && /^    addr:/ { gsub(/"/, "", $$2); sub(/.*:/, "", $$2); print $$2; exit } a && /^  [^ ]/ { a=0 }' $(CONFIG_FILE))

# The printer port stays empty when the printers connect through the API port.
PRINTER_PORT := $(shell awk '/^core:/ { c=1; next } c && /^  printer:/ { p=1; next } p && /^    addr:/ { gsub(/"/, "", $$2); sub(/.*:/, "", $$2); print $$2; exit } p && /^  [^ ]/ { p=0 }' $(CONFIG_FILE))

.DEFAULT_GOAL := help

# These two lines print a clear message when Go or Docker is not installed.
REQUIRE_GO = command -v go >/dev/null 2>&1 || { echo "FAIL  Go is not installed or not on PATH. Install the version named in go.mod from https://go.dev/dl/ and open a new shell."; exit 1; }
REQUIRE_DOCKER = command -v docker >/dev/null 2>&1 || { echo "FAIL  Docker is not installed or not on PATH. This target needs it."; exit 1; }

##@ Start here

help: ## print this list of targets
	@awk 'BEGIN { FS = ":.*##" } /^##@/ { printf "\n%s\n", substr($$0, 5) } /^[a-zA-Z0-9_-]+:.*##/ { printf "  %-26s %s\n", $$1, $$2 }' $(MAKEFILE_LIST)
	@echo ""
	@echo "Helper targets are not listed. To see every target:"
	@echo "  grep -E '^[a-zA-Z0-9_-]+:' Makefile"

# This needs only Go installed. `make build` also builds the web app and the
# documentation site when the configuration asks for them.
run: sync-version ## (go) build the core and run it in the foreground on the address in config.yaml
	@TAGS="$$(go run ./cmd/detect-build-tags --config "$(CONFIG_FILE)" --variant full --extra "$(EXTRA_BUILD_TAGS)")"; \
	echo "Building with tags: full $$TAGS"; \
	go build -tags "full $$TAGS" -ldflags="$(VERSION_LDFLAGS)" -o bin/server ./cmd/prusa-connect
	@echo "Starting the core. Ctrl+C stops it."
	@./bin/server core --config "$(CONFIG_FILE)"

# The demo runs the server together with a few simulated printers, so you can
# try everything without a real printer, a configuration file or an account.
# It is a separate program in cmd/demo, because adding the simulator to the
# real server would make the smallest binary too large. You can pass flags to
# it, for example `make demo DEMO_ARGS="-printers 8"`. It carries the
# diagnostics bundle, as the full release does, so Settings, Diagnostics works.
DEMO_ARGS ?=

# The file extension of a program is `.exe` on Windows and nothing elsewhere.
# This uses `=` instead of `:=` so that make only asks Go for it when a build
# needs it. That way `make help` still works on a machine without Go.
GOEXE = $(shell go env GOEXE)

# This is where the demo looks for the packed example modules.
DEMO_MODULES_DIR ?= bin/demo-modules

# If pnpm is not installed, PACK_OPTIONAL=1 skips the browser parts of the
# modules and the demo still starts.
#
# The second script packs the first-party extensions under extensions/ into
# bin/demo-modules/extensions, so the demo offers them too. It is best effort:
# an extension that does not build prints a warning and is skipped. No CI job
# runs it, so the core's gates never wait for an extension to build.
demo-modules: ## (go, pnpm) build the three shipped modules and, best effort, the extensions under extensions/ into releases under bin/demo-modules
	@$(REQUIRE_GO)
	@PACK_ROOT="$(DEMO_MODULES_DIR)" PACK_OPTIONAL=1 sh scripts/pack-example-modules.sh
	@PACK_ROOT="$(DEMO_MODULES_DIR)" sh scripts/pack-demo-extensions.sh

demo: demo-modules embed-app ## (go, pnpm) run the core with simulated printers and the web app, and offer the example modules and the extensions
	@go build -tags "full diagnostics" -o bin/demo$(GOEXE) ./cmd/demo
	@echo "Starting the demo. Ctrl+C stops the printers and the server."
	@./bin/demo$(GOEXE) -examples "$(DEMO_MODULES_DIR)" $(DEMO_ARGS)

install-deps: _app-deps install-docs-deps ## (pnpm) install the Node dependencies of the web app and the documentation site
	@echo "OK    dependencies installed"

# Git runs no checks until you have run this once.
install-hooks: ## make git run the checks in .githooks before a commit and before a push
	@git config core.hooksPath .githooks
	@chmod +x .githooks/pre-push .githooks/commit-msg 2>/dev/null || true
	@echo "OK    hooks installed from .githooks"
	@echo "  commit-msg: make commit-style-check on the message, and a Changelog: trailer"
	@echo "  pre-push, in the order the hook runs them:"
	@sed -n 's/^make \([a-z-]*\).*/    make \1/p; s/^sh \(scripts\/[a-z.-]*\)/    sh \1/p' .githooks/pre-push

generate-printer-config: ## write a printer configuration file that points a printer at this server
	@bash scripts/generate_printer_config.sh

# You can change the name and the addresses in the certificate with
# API_CERT_CN, API_CERT_DNS and API_CERT_IPS.
certificate-server: ## write the certificate chain the browser and the API listener use
	@bash scripts/generate_certificates.sh server

# A Prusa printer needs the connect.der file to trust this server. You can
# change the name and the addresses with PRUSA_CERT_CN, PRUSA_CERT_DNS and
# PRUSA_CERT_IPS.
certificate-prusa: ## write the certificate chain the printers use, with connect.der
	@bash scripts/generate_certificates.sh prusa

certificate-all: ## write both certificate chains, for the browser and for the printers
	@bash scripts/generate_certificates.sh all

##@ Run the server in the background

# The server writes its process id to .server.pid and its output to server.log.
server-up: build ## (go, pnpm) build the core and start it in the background, writing server.log
	@if [ -f .server.pid ] && ps -p "$$(cat .server.pid)" > /dev/null 2>&1; then \
		echo "The server is already running (PID: $$(cat .server.pid))"; \
		exit 0; \
	fi; \
	rm -f .server.pid; \
	echo "Starting the server..."; \
	./bin/server core --config $(CONFIG_FILE) > server.log 2>&1 & \
	PID=$$!; \
	echo $$PID > .server.pid; \
	sleep 2; \
	if ps -p $$PID > /dev/null 2>&1; then \
		echo "OK    the server is running (PID: $$PID)"; \
		echo "  log:      tail -f server.log"; \
		echo "  health:   curl http://localhost:$(SERVER_PORT)/api/v1/health"; \
		echo "  printers: ws://localhost:$(or $(PRINTER_PORT),$(SERVER_PORT))/p/ws"; \
	else \
		echo "FAIL  the server did not start. Read server.log"; \
		rm -f .server.pid; \
	fi

server-down: ## stop the core started by server-up
	@if [ -f .server.pid ]; then \
		PID=$$(cat .server.pid); \
		if ps -p $$PID > /dev/null 2>&1; then \
			echo "Stopping the core (PID: $$PID)..."; \
			kill $$PID 2>/dev/null || true; \
			sleep 1; \
			if ps -p $$PID > /dev/null 2>&1; then \
				echo "Stopping it the hard way..."; \
				kill -9 $$PID 2>/dev/null || true; \
			fi; \
			rm -f .server.pid; \
			echo "OK    the core is stopped"; \
		else \
			echo "The core is not running"; \
			rm -f .server.pid; \
		fi; \
	else \
		echo "The core is not running (no pid file)"; \
	fi

server-restart: server-down ## stop the core, clear server.log and start it again
	@rm -f server.log
	@echo "OK    server.log cleared"
	@"$(MAKE)" server-up
	@echo "OK    the core is restarted"

status: ## say whether the background core is running and which ports are open
	@if [ -f .server.pid ]; then \
		PID=$$(cat .server.pid); \
		if ps -p $$PID > /dev/null 2>&1; then \
			echo "OK    the core is running (PID: $$PID)"; \
		else \
			echo "FAIL  the core is not running, and .server.pid is stale"; \
			rm -f .server.pid; \
		fi; \
	else \
		echo "FAIL  the core is not running"; \
	fi
	@echo ""
	@echo "Ports:"
	@if command -v lsof > /dev/null 2>&1; then \
		lsof -i :$(SERVER_PORT) $(if $(PRINTER_PORT),-i :$(PRINTER_PORT)) 2>/dev/null | grep LISTEN || echo "  no listener"; \
	else \
		echo "  WARN  lsof is not installed, so no port was read. It is absent on Windows."; \
	fi

logs: ## follow server.log, the log of the background core
	@if [ ! -f server.log ]; then \
		echo "WARN  there is no server.log. Start the core with: make server-up"; \
		exit 1; \
	fi
	@echo "Following server.log. Ctrl+C stops."
	@tail -f server.log

##@ Build

build: build-config ## (go, pnpm) build bin/server, with the web app the configuration asks for

# Every build starts by copying the VERSION file into the Go package that
# embeds it.
#
# The linker then stamps the version into the same package, so the binary
# reports it in its startup log, at /api/v1/health and for --version. On a
# release tag it is the tag. Anywhere else it is VERSION and the commit, as
# scripts/build-version.sh prints it. Set BUILD_VERSION to stamp another value.
BUILD_VERSION ?= $(shell sh scripts/build-version.sh)
UPGRADE_PUBLIC_KEYS ?=
# Where the binary links its documentation. The project is private, so it is
# the beta portal; a public build names the public site. `{version}` in it is
# replaced by the version the binary reports (ADR-20261015-ejz rule 34).
DOCS_URL ?= https://beta.prusa3d.com/connect-local/docs/{version}/

VERSION_LDFLAGS = -X github.com/prusa3d/prusa-connect/internal/kernel/version.stamped=$(BUILD_VERSION) -X github.com/prusa3d/prusa-connect/internal/kernel/version.docsURL=$(DOCS_URL) -X github.com/prusa3d/prusa-connect/internal/upgrade.officialPublicKeys=$(UPGRADE_PUBLIC_KEYS)

sync-version:
	@$(REQUIRE_GO)
	@cp VERSION internal/kernel/version/VERSION

# The configuration file decides which features are built in. You can build for
# another configuration with `make build CONFIG_FILE=config/config.postgres.yaml`.
build-config: sync-version
	@if [ "$$(go run ./cmd/detect-build-feature --config "$(CONFIG_FILE)" --field frontend)" = "true" ]; then \
		"$(MAKE)" embed-app; \
	else \
		echo "Skipping the web app (core.frontend.enabled is false in $(CONFIG_FILE))"; \
	fi
	@TAGS="$$(go run ./cmd/detect-build-tags --config "$(CONFIG_FILE)" --variant full --extra "$(EXTRA_BUILD_TAGS)")"; \
	echo "Building with tags: full $$TAGS"; \
	go build -tags "full $$TAGS" -ldflags="$(VERSION_LDFLAGS)" -o bin/server ./cmd/prusa-connect

# This is the list of every optional feature. build-full, build-ci, test-full
# and deadcode-check all use it, so they always agree.
FULL_TAGS := diagnostics observability postgres $(EXTRA_BUILD_TAGS)

# build-full and build-ci share this compile step. It has to be the last
# prerequisite, so the web app is built before the binary that embeds it. The
# documentation site is not a step of any build: the binary links the
# documentation its build names (DOCS_URL), and `make docs` builds the site.
_build-full-binary:
	@echo "Building with tags: full $(FULL_TAGS)"
	@go build -tags "full $(FULL_TAGS)" -ldflags="$(VERSION_LDFLAGS)" -o bin/server ./cmd/prusa-connect

build-full: sync-version embed-app _build-full-binary ## (go, pnpm) build bin/server with every optional feature and the web app

# CI builds and checks the web app and the documentation in separate jobs, so
# this target leaves them out.
build-ci: sync-version _build-full-binary ## (go) build bin/server with every optional feature, leaving out the web app and the documentation site

# The minimal binary leaves out the embedded web app and the debug symbols. It
# still carries SQLite and the whole HTTP API, because no Go file checks the
# minimal tag.
build-minimal: sync-version ## (go) build bin/server-minimal, the smallest binary
	@TAGS="$$(go run ./cmd/detect-build-tags --config "$(CONFIG_FILE)" --variant minimal --extra "$(EXTRA_BUILD_TAGS)")"; \
	echo "Building with tags: minimal $$TAGS"; \
	CGO_ENABLED=0 go build -tags "minimal $$TAGS" -ldflags="-s -w $(VERSION_LDFLAGS)" -o bin/server-minimal ./cmd/prusa-connect

# The printer build is the binary that runs on the printer's own board, where
# the server has 15 to 20 MB of memory. It carries the full build and the web
# app.
# The printer tag gives it smaller default values and removes nothing. Each tag
# in PRINTER_TAGS leaves one part out. A tag goes in only after the owner
# confirms that part. The comment on its line names the rule of
# ADR-20260925-gv7 that leaves it out. internal/arch checks both, and it
# refuses these tags in every other build.
#
# PRINTER_CONFIG is the configuration the printer's operating system installs
# beside the binary, and the build takes its feature tags from it.
# PRINTER_ARCH=amd64 builds a copy that runs on a PC, for a measurement.
#
# PRINTER_MODEL names the one printer model whose capability profile the
# binary embeds, as internal/printer/profile/profiles.json spells it. It has
# no default, so make build-printer stops until it is given. The gates are not
# given the printer's model, so make test-unit and the binary budget write the
# profile of the released model PRINTER_CHECK_MODEL names.
PRINTER_CONFIG := config/examples/config.printer.yaml
PRINTER_ARCH ?= arm64
PRINTER_MODEL ?=
PRINTER_CHECK_MODEL := MK4S
PRINTER_TAGS :=
PRINTER_TAGS += noreference # ADR-20260925-gv7 rule 27
PRINTER_TAGS += nodevtools # ADR-20260925-gv7 rule 28
PRINTER_TAGS += noothermodels # ADR-20260925-gv7 rule 29

# This stops the printer build before the web app is built, when nobody named
# the printer's model or the printer's configuration is missing.
_printer-build-inputs:
	@if [ -z "$(PRINTER_MODEL)" ]; then echo "FAIL  PRINTER_MODEL is not set. Name the printer's model: make build-printer PRINTER_MODEL=<model>"; exit 2; fi
	@if [ ! -f "$(PRINTER_CONFIG)" ]; then echo "FAIL  $(PRINTER_CONFIG) is missing. The printer build reads its feature tags from it."; exit 2; fi

# The linker writes the model into the binary, and the binary refuses to start
# when the embedded profile belongs to another model.
PRINTER_MODEL_FLAG = -X github.com/prusa3d/prusa-connect/internal/printer/profile.BuiltFor=

build-printer: _printer-build-inputs sync-version embed-app ## (go, pnpm) build bin/server-printer, the static linux binary for the printer's own board
	@go run ./cmd/extract-printer-profile -model "$(PRINTER_MODEL)"
	@TAGS="$$(go run ./cmd/detect-build-tags --config "$(PRINTER_CONFIG)" --variant full)"; \
	echo "Building the printer binary for linux/$(PRINTER_ARCH) with tags: full printer $(strip $(PRINTER_TAGS)) $$TAGS"; \
	CGO_ENABLED=0 GOOS=linux GOARCH=$(PRINTER_ARCH) go build -tags "full printer $(strip $(PRINTER_TAGS)) $$TAGS" -ldflags="-s -w $(VERSION_LDFLAGS) $(PRINTER_MODEL_FLAG)$(PRINTER_MODEL)" -o bin/server-printer ./cmd/prusa-connect

# Build for another processor:
#
#   make build-cross ARCH=<arch> [VARIANT=full|minimal]
#
# ARCH is amd64, aarch64 (64-bit Raspberry Pi 3 and later, Zero 2 W, Home
# Assistant OS), armv7 (32-bit Raspberry Pi 2 and later, Zero 2 W) or armhf
# (Raspberry Pi Zero and 1, ARMv6).
# The full variant builds with Docker, in .github/dockerfiles/Dockerfile.build.
# The minimal variant is a plain `go build` and needs no Docker. The output is
# bin/prusa_connect[_minimal]_<arch>.
ARCH ?= amd64
VARIANT ?= full

# The Go version comes from go.mod and is passed to Docker, so the two always
# match.
GO_VERSION := $(shell awk '/^go /{print $$2; exit}' go.mod)

# These tables map each ARCH to a Docker platform and to GOARCH and GOARM.
_DOCKER_PLATFORM_amd64    := linux/amd64
_DOCKER_PLATFORM_aarch64  := linux/arm64
_DOCKER_PLATFORM_armv7    := linux/arm/v7
_DOCKER_PLATFORM_armhf    := linux/arm/v6
_GOARCH_amd64    := amd64
_GOARCH_aarch64  := arm64
_GOARCH_armv7    := arm
_GOARCH_armhf    := arm
_GOARM_armv7     := 7
_GOARM_armhf     := 6

build-cross: ## (go, docker for VARIANT=full) build a binary for ARCH=amd64|aarch64|armv7|armhf, VARIANT=full|minimal
	@if [ -z "$(ARCH)" ]; then echo "FAIL  ARCH is required: amd64, aarch64, armv7 or armhf"; exit 2; fi
	@case "$(ARCH)" in amd64|aarch64|armv7|armhf) :;; *) echo "FAIL  ARCH=$(ARCH) is not one of amd64, aarch64, armv7, armhf"; exit 2;; esac
	@case "$(VARIANT)" in full|minimal) :;; *) echo "FAIL  VARIANT must be full or minimal"; exit 2;; esac
	@mkdir -p bin
	@if [ "$(VARIANT)" = "full" ]; then \
		"$(MAKE)" _build-cross-full ARCH=$(ARCH); \
	else \
		"$(MAKE)" _build-cross-minimal ARCH=$(ARCH); \
	fi

_build-cross-full:
	@sh scripts/build-cross-full.sh "$(ARCH)" "$(_DOCKER_PLATFORM_$(ARCH))" "$(GO_VERSION)" "$(BUILD_VERSION)"

_build-cross-minimal:
	@echo "Building the minimal binary for $(ARCH) (GOARCH=$(_GOARCH_$(ARCH))$(if $(_GOARM_$(ARCH)), GOARM=$(_GOARM_$(ARCH))))..."
	GOOS=linux GOARCH=$(_GOARCH_$(ARCH)) $(if $(_GOARM_$(ARCH)),GOARM=$(_GOARM_$(ARCH))) CGO_ENABLED=0 \
		go build -tags minimal -ldflags="-s -w $(VERSION_LDFLAGS)" -o bin/prusa_connect_minimal_$(ARCH) ./cmd/prusa-connect
	@echo "OK    bin/prusa_connect_minimal_$(ARCH)"
	@echo "  it serves the HTTP API, WebSocket and the memory queue"
	@echo "  it leaves out MQTT, SSE, sqlite, the web app and the metrics"

print-build-tags: ## print the feature tags a build from CONFIG_FILE would use
	@echo "$$(go run ./cmd/detect-build-tags --config "$(CONFIG_FILE)" --variant full --extra "$(EXTRA_BUILD_TAGS)")"

# This builds a server from a build file that lists the core and the adapters
# to include, without checking them out. To add an adapter, edit that file.
# You can name the file with BUILD_FILE or with CONFIG; CONFIG wins when both
# are set.
BUILD_FILE ?= examples/compose/build.yaml
CONFIG ?=
compose-build: ## (go) build a binary from BUILD_FILE (or CONFIG), pulling in the adapters it names
	@go run ./cmd/compose-build --config "$(if $(CONFIG),$(CONFIG),$(BUILD_FILE))"

# This test builds a server with an adapter from outside this repository and
# checks that the running server serves it. It is not part of the unit tests,
# because it builds the whole server.
compose-check: ## (go) compose the fixture adapter into a binary and prove the server serves it
	@go test -count=1 -timeout 20m ./test/compose/

# The JavaScript targets need pnpm. If it is missing, scripts/ensure-pnpm.sh
# offers to install it. AUTO_INSTALL_PNPM=1 answers yes without asking.
AUTO_INSTALL_PNPM ?=

_ensure-pnpm:
	@sh scripts/ensure-pnpm.sh "$(AUTO_INSTALL_PNPM)"

# This deletes only build output. It never touches data/, storage/, files/,
# config/config.d/ or the database, because those hold a running server's
# printers, uploads and permissions.
clean: clean-js clean-frontend clean-docs clean-app ## delete what a build wrote, and nothing an operator's server wrote
	rm -rf bin/
	rm -f .server.pid server.log server-e2e.log server-sim.log

# These are the JavaScript projects other than the web app and the
# documentation site. The two have their own clean targets below, because each
# has a cache directory as well.
JS_PROJECTS := packages/typescript packages/svelte packages/typescript/testdata/consumers/* packages/svelte/build/testdata/module examples/spools/ui examples/spools/client examples/nozzle-watch/ui extensions/fleet-stats/ui examples/service-plugin/client packages/fractal/client

clean-js:
	@for DIR in $(JS_PROJECTS); do rm -rf "$$DIR/node_modules" "$$DIR/dist" "$$DIR/storybook-static"; done
	@echo "OK    JavaScript build output removed"

clean-frontend:
	@if [ -d "$(APP_DIR)" ]; then \
		cd $(APP_DIR) && rm -rf node_modules dist .astro; \
	fi

# docs/public/swagger is generated by the docs build, so it is removed as well.
clean-docs:
	@if [ -d "docs" ]; then \
		cd docs && rm -rf node_modules dist .astro public/swagger; \
	fi

##@ Test

test: test-unit ## (go) the unit suite of both modules
	@echo "OK    the unit suite passed"

# The sdk and every module under packages/ are separate Go modules, so each one
# gets its own `go test` run.
#
# Some test files only compile with a build tag. Without the tag, Go skips
# them silently and still prints ok. So the packages with such tests are run a
# second time with their tag. A test in internal/arch checks that none is
# forgotten here. Write the package paths out in full, because that test reads
# this file as text.
#
# AUTHZ_REQUIRED_ENGINES=sqlite makes a missing sqlite engine a failure instead
# of a skip. Postgres is only required in test-engines, because it needs a
# container.
#
# The timeout is twenty minutes, because internal/app is large and a slow CI
# runner needs more than the default ten.
test-unit: ## (go) the unit suite of every module, tag-gated packages included
	@echo "Running the unit tests..."
	go test -short -timeout 20m ./...
	@echo "Running the tests that need a build tag..."
	AUTHZ_REQUIRED_ENGINES=sqlite go test -short -count=1 ./internal/authz/... ./adapters/db/...
	go test -short -count=1 -timeout 20m -tags observability ./internal/app/ ./internal/platform/observability/prom/
	go test -short -count=1 -timeout 20m -tags diagnostics ./internal/app/ ./internal/app/resource/ ./internal/transport/http/httpapi/
	"$(MAKE)" test-printer
	@echo "Running the sdk unit tests..."
	cd sdk && go test -short ./...
	@echo "Running the unit tests of every module under packages/..."
	@for mod in packages/*/go.mod; do \
		dir=$${mod%/go.mod}; \
		echo "-> $$dir"; \
		( cd "$$dir" && go test -short -count=1 ./... ) || exit 1; \
	done

# The race detector needs a C compiler. On a machine without one, this target
# runs the tests in a Docker container instead (see test-race-linux). It always
# prints which of the two it did. Without a compiler and without Docker it
# fails, because it cannot run at all.
#
# The whole tree takes minutes. To run one package:
# `make test-race RACE_PACKAGES=./internal/app/`.
RACE_PACKAGES ?= ./...

# These are the same build tags CI uses, so the run covers the same test files.
RACE_TAGS ?= full diagnostics observability postgres

test-race: ## (docker, where the machine has no C compiler) unit tests under the race detector, every module
	@CC="$$(go env CC)"; \
	if command -v "$$CC" > /dev/null 2>&1; then \
		echo "Race detector: running on this machine with $$CC."; \
		CGO_ENABLED=1 go test -short -race -count=1 -timeout $(TEST_TIMEOUT) -tags "$(RACE_TAGS)" $(RACE_PACKAGES) || exit $$?; \
		echo "Race detector: sdk module..."; \
		(cd sdk && CGO_ENABLED=1 go test -short -race -count=1 ./...) || exit $$?; \
		for mod in packages/*/go.mod; do \
			dir=$${mod%/go.mod}; \
			echo "Race detector: $$dir module..."; \
			(cd "$$dir" && CGO_ENABLED=1 go test -short -race -count=1 ./...) || exit $$?; \
		done; \
		exit 0; \
	fi; \
	echo "Race detector: there is no C compiler on this machine ($$CC is not on PATH)."; \
	if ! command -v docker > /dev/null 2>&1; then \
		echo "FAIL  Docker is not installed either, so the race detector cannot run."; \
		exit 1; \
	fi; \
	echo "Race detector: running in Docker instead."; \
	"$(MAKE)" test-race-linux PACKAGES=$(RACE_PACKAGES) TAGS="$(RACE_TAGS)"

# This copies the tree into a Docker image and runs the race detector there,
# with the Postgres test database from the compose file. The database stays up
# afterwards; `make fixtures-down` removes it.
#
# PACKAGES is passed to go test and TAGS are the build tags. TAGS="" runs
# without tags. A path under ./sdk/ runs inside the sdk module.
PACKAGES ?= ./...
TAGS ?= full diagnostics observability postgres
COMPOSE_PROJECT := prusa-connect

# The Go caches live in two named volumes, so a second run does not download
# and compile everything again. `docker compose down -v` leaves them alone, and
# docker-clean keeps them because of the label.
RACE_VOLUME_LABELS := --label "com.docker.compose.project=$(COMPOSE_PROJECT)" --label "com.prusa3d.keep=a cache the race runs share"

test-race-linux: ## (docker) one package or the whole tree under the race detector on Linux, against the Postgres fixture
	@$(REQUIRE_DOCKER)
	@docker volume create $(RACE_VOLUME_LABELS) prusa_connect_race_modcache > /dev/null
	@docker volume create $(RACE_VOLUME_LABELS) prusa_connect_race_buildcache > /dev/null
	@echo "Race detector: building the Docker image..."
	@docker compose -f $(PGFIXTURE_COMPOSE) build race
	@echo "Race detector: running PACKAGES=$(PACKAGES) TAGS=\"$(TAGS)\""
	@PACKAGES="$(PACKAGES)" TAGS="$(TAGS)" docker compose -f $(PGFIXTURE_COMPOSE) run --rm race

# This runs the database tests against sqlite and a real Postgres. Without a
# Postgres connection, Go would skip the Postgres tests and still print ok, so
# AUTHZ_REQUIRED_ENGINES makes a missing engine a failure. internal/app is
# included because the migration and the start-up cleanup only run when the
# server starts.
#
# The Postgres container comes from the local compose file. --no-deps starts
# only that container and leaves a running server alone.
PGFIXTURE_COMPOSE := docker-compose.local.yml
PGFIXTURE_PORT ?= 55432
PGFIXTURE_USER ?= authz
PGFIXTURE_PASSWORD ?= authz
PGFIXTURE_DB ?= authz
PGFIXTURE_DSN := postgres://$(PGFIXTURE_USER):$(PGFIXTURE_PASSWORD)@127.0.0.1:$(PGFIXTURE_PORT)/$(PGFIXTURE_DB)?sslmode=disable

test-engines: ## (go, docker) the authorization suite against sqlite and a real Postgres, both required
	@$(REQUIRE_DOCKER)
	@echo "Starting the Postgres container..."
	@docker compose -f $(PGFIXTURE_COMPOSE) up -d --wait --no-deps postgres-fixture
	@echo "Running the tests against sqlite and Postgres..."
	@CGO_ENABLED=0 \
		AUTHZ_TEST_POSTGRES_DSN="$(PGFIXTURE_DSN)" \
		AUTHZ_REQUIRED_ENGINES=sqlite,postgres \
		go test -count=1 -tags postgres ./internal/authz/... ./adapters/db/... ./internal/platform/auth/dbstore/... ./internal/platform/dashboards/... ./internal/platform/devicestore/... ./internal/platform/instanceid/... ./internal/app/ ./internal/app/hostschema/ ./examples/database-module/

test-engines-down: ## (docker) stop and remove the Postgres fixture
	@$(REQUIRE_DOCKER)
	@docker compose -f $(PGFIXTURE_COMPOSE) rm -sfv postgres-fixture

# The whole life of a module's own database (ADR-20261021-bqc), on the built server
# with examples/database-module: install, restart, updates with a schema and a
# data migration, a failed update taken back, a refused downgrade, removal,
# reinstall and purge, on each layout of the core and the module over SQLite
# and two Postgres containers. It starts the containers itself, names them
# pcl-moddb-e2e-*, and removes them with their volumes when it ends.
module-database-e2e: ## (go, docker) a module's own database through its whole life, on every layout
	@$(REQUIRE_DOCKER)
	@PCL_MODULE_DATABASE_E2E=1 go test -count=1 -timeout 40m -v ./test/moduledatabase/

# Builds separate old, successful and failing core executables. The server is
# driven through its public maintenance API, and the suite observes the real
# executable replacement, trial child, final exec and rollback snapshot.
# One journey also starts the last release tagged before HEAD, so it needs the
# tags. A tag run of the release workflow gets the release before the one it
# builds. A full clone with no release tag but those on HEAD is the first
# release of its repository, and `first` tells the journey there is none.
PCL_UPGRADE_E2E_FROM ?= $(shell git describe --tags --abbrev=0 --match 'v[0-9]*' HEAD^ 2>/dev/null || \
	{ [ "$$(git rev-parse --is-shallow-repository)" = false ] && [ -z "$$(git tag -l 'v[0-9]*' --no-contains HEAD)" ] && echo first; })
upgrade-process-e2e: ## (go, Unix) automatic core replacement and rollback through real processes
	@PCL_UPGRADE_E2E_FROM="$(PCL_UPGRADE_E2E_FROM)" go test -count=1 -timeout 12m -v ./test/upgradeprocess/

# The three message brokers the event adapters are tested against. They are
# started for the run and removed afterwards.
FIXTURES := mosquitto-fixture rabbitmq-fixture redpanda-fixture

test-adapters: ## (go, docker) the adapter suites, and the core's MQTT tool topics, against real brokers started and removed for the run
	@echo "Starting the brokers..."
	@docker compose -f $(PGFIXTURE_COMPOSE) up -d --wait --no-deps $(FIXTURES)
	@echo "Running the adapter tests..."
	@go test -count=1 ./adapters/... && \
		go test -count=1 -run '^TestASimulatedPrintersToolsReachTheBroker' ./internal/app/ ; RC=$$? ; \
		echo "Removing the brokers..." ; \
		docker compose -f $(PGFIXTURE_COMPOSE) rm -sfv $(FIXTURES) >/dev/null ; \
		exit $$RC

# Starts every test container at once, for a session of manual work.
fixtures-up: ## (docker) start every test fixture: the brokers, Postgres, the OIDC server and the npm registry
	@docker compose -f $(PGFIXTURE_COMPOSE) up -d --wait --no-deps $(FIXTURES) postgres-fixture mock-oidc npm-registry-fixture
	@docker compose -f $(PGFIXTURE_COMPOSE) ps --format "  {{.Name}}	{{.Status}}"

fixtures-down: ## (docker) stop and remove every test fixture
	@docker compose -f $(PGFIXTURE_COMPOSE) rm -sfv $(FIXTURES) postgres-fixture mock-oidc npm-registry-fixture

# The developer Postgres: a database for the core and one for a module, each
# with a login of its own (ADR-20261021-bqc). It keeps its data in a volume, and only
# postgres-dev-reset deletes it. The passwords are the defaults in .env.example.
PGDEV_PORT ?= 55433
PGDEV_VOLUME := prusa_connect_postgres_dev_data

postgres-dev-up: ## (docker) start the developer Postgres with a database for the core and one for a module
	@docker compose -f $(PGFIXTURE_COMPOSE) up -d --wait --no-deps postgres-dev
	@echo "OK    the developer Postgres is up"
	@echo "      core:   postgres://connect:connect@127.0.0.1:$(PGDEV_PORT)/connect?sslmode=disable"
	@echo "      module: postgres://notes:notes@127.0.0.1:$(PGDEV_PORT)/notes?sslmode=disable"

postgres-dev-down: ## (docker) stop and remove the developer Postgres, keeping its data
	@docker compose -f $(PGFIXTURE_COMPOSE) rm -sf postgres-dev

postgres-dev-reset: ## (docker) remove the developer Postgres and every database it holds
	@docker compose -f $(PGFIXTURE_COMPOSE) rm -sf postgres-dev && docker volume rm -f $(PGDEV_VOLUME) > /dev/null && echo "OK    the developer Postgres holds nothing"

# These tests drive every printer model through the real driver, at every
# firmware version we support. The full set takes a few seconds and CI runs it
# on every commit. The fast set covers one firmware version per model. To add a
# model, add a row to internal/printer/profile/profiles.json; to add a firmware
# version, add a line to test/fleet/releases.json.
test-matrix: ## (go) every printer model at one firmware version each, the fast set
	@echo "Model and firmware matrix, fast set"
	@PCL_MATRIX=fast go test -count=1 ./test/fleet/...

test-matrix-full: ## (go) every printer model at every firmware version, with the storage and delay faults
	@echo "Model and firmware matrix, full set"
	@PCL_MATRIX=full go test -count=1 ./test/fleet/...
	@echo "Storage and response-delay faults, every model, on the wire"
	@go test -count=1 -run "TestEveryModel" ./internal/printer/simulator/...

# These tests build the server binary and run it with a real configuration
# file, a simulated printer on a real socket and a real event stream. Nothing
# needs to be running first.
test-e2e: ## (go) end to end, against the built binary over real sockets
	@echo "End to end, against the built binary"
	@go test -count=1 ./test/e2e/...

# This boots real MK4 firmware in the MINI404 emulator and checks it through
# the server's HTTP API. It runs on demand only, because the emulator is slow
# and sometimes flaky. scripts/build-mini404.sh and scripts/build-firmware.sh
# build the emulator and the firmware image.
test-firmware: ## (go, MINI404 and a firmware image) real firmware under the emulator, asserted through the API
	@echo "Firmware in the loop, on real firmware under MINI404"
	@PCL_FIRMWARE=1 go test -count=1 -timeout 45m ./test/firmware/...

# This boots one emulated printer, drives it end to end and stops it. A cold
# boot takes minutes, so it runs on demand only.
mini404-smoke: ## (go, MINI404 and a firmware image) one emulated printer against a live HTTP stack
	@echo "MINI404 smoke, one emulated printer to a live HTTP stack"
	@go run ./tools/mini404smoke $(MINI404_SMOKE_ARGS)

# This boots one emulated printer and connects it to a server that is already
# running, until you press Ctrl+C. The server address goes into the printer's
# EEPROM before it boots. To use another address:
# FIRMWARE_UP_ARGS="-server 127.0.0.1:8081".
firmware-up: ## (go, MINI404 and a firmware image) one emulated printer attached to a running server, until Ctrl+C
	@echo "One emulated printer on real firmware, attached to a running server"
	@go run ./tools/firmwareprinter $(FIRMWARE_UP_ARGS)

# The printer build compiles with tags no other build passes, so its tests run
# here under the same tags. The packages the tags change run whole. The server
# package and the profile package run only their printer tests, because the
# rest of their tests read simulated printers of every model. The profile file
# comes from PRINTER_CHECK_MODEL, because the gates are not given the printer's
# model.
PRINTER_TEST_TAGS = full printer $(PRINTER_TAGS) $$(go run ./cmd/detect-build-tags --config "$(PRINTER_CONFIG)" --variant full)
PRINTER_APP_TESTS := MemoryLimit|PageCache|PreviewCache|PasswordCost|ABuildWithoutDashboards

test-printer: ## (go) the tests the printer build's tags change, under those tags
	@echo "Running the tests under the printer build's tags..."
	@go run ./cmd/extract-printer-profile -model "$(PRINTER_CHECK_MODEL)"
	go test -short -count=1 -tags "$(PRINTER_TEST_TAGS)" -ldflags "$(PRINTER_MODEL_FLAG)$(PRINTER_CHECK_MODEL)" ./internal/kernel/buildprofile/ ./internal/app/config/... ./internal/platform/auth/... ./internal/transport/http/httpapi/ ./adapters/db/...
	go test -short -count=1 -tags "$(PRINTER_TEST_TAGS)" -ldflags "$(PRINTER_MODEL_FLAG)$(PRINTER_CHECK_MODEL)" -run '$(PRINTER_APP_TESTS)' ./internal/app/
	go test -short -count=1 -tags "$(PRINTER_TEST_TAGS)" -ldflags "$(PRINTER_MODEL_FLAG)$(PRINTER_CHECK_MODEL)" -run 'TestThePrinter' ./internal/printer/profile/

test-minimal: ## (go) the unit suite with the minimal build tags
	@echo "Running the tests with the minimal build tags..."
	@TAGS="$$(go run ./cmd/detect-build-tags --config "$(CONFIG_FILE)" --variant minimal --extra "$(EXTRA_BUILD_TAGS)")"; \
	echo "Testing with tags: minimal $$TAGS"; \
	CGO_ENABLED=0 go test -tags "minimal $$TAGS" -short ./...

test-config: ## (go) the unit suite with the tags CONFIG_FILE asks for
	@echo "Running the tests with the tags the configuration asks for..."
	@TAGS="$$(go run ./cmd/detect-build-tags --config "$(CONFIG_FILE)" --variant full --extra "$(EXTRA_BUILD_TAGS)")"; \
	echo "Testing with tags: full $$TAGS"; \
	go test -tags "full $$TAGS" -short ./...

test-full: ## (go) the unit suite with every optional feature tag
	@echo "Running the tests with every optional feature tag"
	@echo "Testing with tags: full $(FULL_TAGS)"
	@go test -tags "full $(FULL_TAGS)" -short ./...

# Coverage of the unit tests and the integration test, in one report. Do not
# pipe a test run into grep, head or tail here: the pipe would hide a failed
# run, and this make version has no pipefail. All reports go under bin/, because
# a test refuses build output in the repository root.
COVERAGE_UNIT_PROFILE := bin/coverage_unit.out
COVERAGE_INTEGRATION_PROFILE := bin/coverage_integration.out
COVERAGE_REPORT := bin/coverage.out
COVERAGE_HTML := bin/coverage.html
COVERAGE_INTEGRATION_HTML := bin/coverage_integration.html

# The integration coverage is measured over one test: a simulated printer
# connects to the server, registers and takes a command over the API. The
# server runs inside the test process, so its code is measured. Only the `full`
# tag is needed for it. scripts/coverage.sh explains the ways this measurement
# can silently measure nothing, and how the script guards against each.
COVERAGE_INTEGRATION_TAGS := full
COVERAGE_INTEGRATION_TEST := TestASimulatedPrinterDialsInAndTakesACommand
COVERAGE_INTEGRATION_PKG := ./internal/app
COVERAGE_INTEGRATION_LOG := bin/coverage_integration.log

coverage: ## (go) the unit and integration runs together, as one coverage report under bin/
	@COVERAGE_UNIT_PROFILE="$(COVERAGE_UNIT_PROFILE)" COVERAGE_INTEGRATION_PROFILE="$(COVERAGE_INTEGRATION_PROFILE)" COVERAGE_REPORT="$(COVERAGE_REPORT)" COVERAGE_HTML="$(COVERAGE_HTML)" COVERAGE_INTEGRATION_LOG="$(COVERAGE_INTEGRATION_LOG)" COVERAGE_INTEGRATION_TAGS="$(COVERAGE_INTEGRATION_TAGS)" COVERAGE_INTEGRATION_TEST="$(COVERAGE_INTEGRATION_TEST)" COVERAGE_INTEGRATION_PKG="$(COVERAGE_INTEGRATION_PKG)" sh scripts/coverage.sh

# The exit status of the test run is kept and reported after the report is
# written, so a failed run still fails the target.
coverage-integration: ## (go) coverage over the one integration case, with the server in process
	@echo "Running the integration test with coverage..."
	@mkdir -p bin
	@if [ "$$(go test -list '^$(COVERAGE_INTEGRATION_TEST)$$' $(COVERAGE_INTEGRATION_PKG) | grep -c '^$(COVERAGE_INTEGRATION_TEST)$$')" -eq 0 ]; then \
		echo "FAIL  there is no test named $(COVERAGE_INTEGRATION_TEST) in $(COVERAGE_INTEGRATION_PKG)."; \
		exit 1; \
	fi
	@go test -tags "$(COVERAGE_INTEGRATION_TAGS)" -v -coverprofile=$(COVERAGE_INTEGRATION_PROFILE) -coverpkg=./... $(COVERAGE_INTEGRATION_PKG) -run '^$(COVERAGE_INTEGRATION_TEST)$$' -short=false > $(COVERAGE_INTEGRATION_LOG) 2>&1; \
	INTEGRATION=$$?; \
	cat $(COVERAGE_INTEGRATION_LOG); \
	if ! grep -q "^--- PASS: $(COVERAGE_INTEGRATION_TEST)" $(COVERAGE_INTEGRATION_LOG); then \
		echo "FAIL  $(COVERAGE_INTEGRATION_TEST) did not pass (a skip counts as a failure here), so the report measures nothing."; \
		INTEGRATION=1; \
	fi; \
	if [ -f $(COVERAGE_INTEGRATION_PROFILE) ]; then \
		go tool cover -html=$(COVERAGE_INTEGRATION_PROFILE) -o $(COVERAGE_INTEGRATION_HTML); \
		TOTAL="$$(go tool cover -func=$(COVERAGE_INTEGRATION_PROFILE))" || exit $$?; \
		printf '%s\n' "$$TOTAL" | tail -1; \
	else \
		echo "No coverage data was written, because no test ran"; \
	fi; \
	if [ $$INTEGRATION -ne 0 ]; then \
		echo ""; \
		echo "FAIL  the coverage report was written, but the tests failed."; \
		exit 1; \
	fi

# This starts the demo and drives a real browser through the whole customer
# journey: sign in, upload a file, see its picture, print it on a chosen
# printer and watch the job on the printer's page. The browser test is under
# frontend/test/journey.
journey-check: ## (go, pnpm, node, curl, a Playwright Chromium) a person uploads a file, prints it and watches the job, against a running core
	bash scripts/check-journey.sh

# This walks the first-run wizard to the sign-in at a real OpenID Connect
# provider, through the restart, and signs people in and out at Keycloak,
# authentik and Authelia, each in a container. The browser test is under
# frontend/test/oidc. Outside CI it runs one provider, the lightest this
# machine can run: Authelia on Linux, Keycloak elsewhere. Name providers to run
# only those, or `all` for the three:
# `make oidc-providers-check OIDC_PROVIDERS=keycloak`.
OIDC_PROVIDERS ?=

oidc-providers-check: ## (go, pnpm, node, curl, docker, a Playwright Chromium) the wizard, the sign-in and the sign-out at a real OpenID Connect provider, OIDC_PROVIDERS=all for all three
	sh scripts/check-oidc-providers.sh $(OIDC_PROVIDERS)

# Before you connect a printer with an ethernet cable, this checks the network
# adapter, the firewall and which process holds the port. The script is
# PowerShell, so it only runs on Windows. Pass arguments with PREFLIGHT_ARGS,
# for example `make preflight PREFLIGHT_ARGS="-Port 80 -AdapterName 'Ethernet 2'"`.
PREFLIGHT_SCRIPT := scripts/bench-preflight.ps1
PREFLIGHT_ARGS ?=

preflight: ## (windows) read the cable, the address, the firewall, the bind address and which server holds the port
	@if [ ! -f $(PREFLIGHT_SCRIPT) ]; then \
		echo "FAIL  $(PREFLIGHT_SCRIPT) is missing."; \
		exit 1; \
	fi; \
	if command -v pwsh > /dev/null 2>&1; then \
		echo "Preflight: running $(PREFLIGHT_SCRIPT) through pwsh."; \
		exec pwsh -NoProfile -ExecutionPolicy Bypass -File $(PREFLIGHT_SCRIPT) $(PREFLIGHT_ARGS); \
	fi; \
	if command -v powershell > /dev/null 2>&1; then \
		echo "Preflight: running $(PREFLIGHT_SCRIPT) through Windows PowerShell."; \
		exec powershell -NoProfile -ExecutionPolicy Bypass -File $(PREFLIGHT_SCRIPT) $(PREFLIGHT_ARGS); \
	fi; \
	echo "FAIL  neither pwsh nor powershell is on PATH, so $(PREFLIGHT_SCRIPT) did not run. It only works on Windows."; \
	exit 1

# CI splits the unit tests into six parallel jobs, called shards, because one
# job over the whole tree took twenty minutes. The three slowest packages get a
# shard each, the demo tests get one because they start a whole server, the
# other packages under internal/ get one and every package outside it the last.

# internal/app can take over fifteen minutes under the race detector on a CI
# runner, so the default of ten is not enough. test-race uses the same value.
TEST_TIMEOUT ?= 30m

# The shards build with the same tags as the race detector, so they cover the
# same test files.
SHARD_TAGS := $(RACE_TAGS)

# The internal shard is every package under internal/ that does not match this
# pattern, and the rest shard every package outside internal/, so a package is
# never in two shards or in none. Two shards rather than one, because building
# over a hundred test binaries on a runner's two processors is most of their
# time.
SHARD_HEAVY_PATTERN := /(internal/app|internal/transport/http/httpapi|internal/authz)$$

# The app shard skips these test names and the demo shard runs only them.
SHARD_DEMO_PATTERN := ^TestTheDemo|^TestTheIndexGoesWhereTheDemoDeclaresItsModules

# SHARD has no default on purpose. An unknown name must fail, because a CI job
# that ran no tests would look green.
SHARD ?=

# BUILD=minimal runs the shard with the minimal build tags and no cgo, which is
# what `make test-minimal` runs over the whole tree. CI runs the minimal build
# as these shards, so it reads the same package lists as the unit and race
# lanes. Empty builds with SHARD_TAGS.
BUILD ?=

# PART=k/n runs the k-th of n parts of the app shard: its test names, as
# `go test -list` prints them, dealt out in turn. The package spends its time
# waiting on servers and flushes, two tests at a time on a runner's two
# processors, so parts run beside each other finish sooner. A higher -parallel
# would wait less too, but eight at a time on two processors fails tests whose
# budgets are tens of milliseconds to a second, in internal/app and in
# internal/connector/xbuddy. A part runs each test as the whole shard does.
# Every test lands in exactly one part, and a part with no test fails.
PART ?=

# RACE=1 runs the shard under the race detector, which needs cgo and so a C
# compiler. `make test-race` is the target that falls back to Docker without one.
RACE ?=

test-shard: ## (go) one shard of the unit suite, named by SHARD=app|demo|httpapi|authz|internal|rest, PART=k/n for part of app, BUILD=minimal for the minimal build
	@case "$(BUILD)" in \
	"") TAGS='$(SHARD_TAGS)' ;; \
	minimal) \
		if [ "$(RACE)" = "1" ]; then echo "FAIL  BUILD=minimal builds without cgo and RACE=1 needs it."; exit 1; fi; \
		TAGS="minimal $$(go run ./cmd/detect-build-tags --config "$(CONFIG_FILE)" --variant minimal --extra "$(EXTRA_BUILD_TAGS)")"; \
		export CGO_ENABLED=0 ;; \
	*) echo "FAIL  BUILD='$(BUILD)' is not a build. Leave it empty, or BUILD=minimal."; exit 1 ;; \
	esac; \
	case "$(SHARD)" in \
	app) PKGS="./internal/app/"; RUN=""; SKIP='$(SHARD_DEMO_PATTERN)' ;; \
	demo) PKGS="./internal/app/"; RUN='$(SHARD_DEMO_PATTERN)'; SKIP="" ;; \
	httpapi) PKGS="./internal/transport/http/httpapi/"; RUN=""; SKIP="" ;; \
	authz) PKGS="./internal/authz/..."; RUN=""; SKIP="" ;; \
	internal) PKGS="$$(go list -tags "$$TAGS" ./internal/... | grep -vE '$(SHARD_HEAVY_PATTERN)')"; RUN=""; SKIP="" ;; \
	rest) PKGS="$$(go list -tags "$$TAGS" ./... | grep -v "^$$(go list -m)/internal/")"; RUN=""; SKIP="" ;; \
	*) echo "FAIL  SHARD='$(SHARD)' is not a shard. The shards are app, demo, httpapi, authz, internal and rest."; exit 1 ;; \
	esac; \
	if [ -z "$$PKGS" ]; then \
		echo "FAIL  shard '$(SHARD)' contains no package."; \
		exit 1; \
	fi; \
	if [ -n "$(PART)" ]; then \
		if [ "$(SHARD)" != app ]; then echo "FAIL  PART splits the app shard, not '$(SHARD)'."; exit 1; fi; \
		case "$(PART)" in [1-9]/[1-9]|[1-9]/[1-9][0-9]) ;; *) echo "FAIL  PART='$(PART)' is not k/n."; exit 1 ;; esac; \
		K="$(PART)"; K="$${K%%/*}"; N="$(PART)"; N="$${N##*/}"; \
		if [ "$$K" -gt "$$N" ]; then echo "FAIL  PART='$(PART)' names a part past the last."; exit 1; fi; \
		LIST="$$(go test -tags "$$TAGS" -list '.*' $$PKGS)" || { echo "$$LIST"; echo "FAIL  the tests of the app shard could not be listed."; exit 1; }; \
		NAMES="$$(echo "$$LIST" | grep -E '^(Test|Example|Fuzz)' | grep -vE '$(SHARD_DEMO_PATTERN)' | awk -v k="$$K" -v n="$$N" '(NR-1)%n==k-1')"; \
		if [ -z "$$NAMES" ]; then echo "FAIL  part $(PART) of the app shard holds no test."; exit 1; fi; \
		RUN="^($$(echo $$NAMES | tr ' ' '|'))$$"; SKIP=""; \
		echo "Shard $(SHARD), part $(PART): $$(echo $$NAMES | wc -w) test(s)"; \
	fi; \
	if [ -n "$$RUN" ]; then set -- -run "$$RUN"; elif [ -n "$$SKIP" ]; then set -- -skip "$$SKIP"; else set --; fi; \
	if [ "$(RACE)" = "1" ]; then set -- -race "$$@"; export CGO_ENABLED=1; echo "Shard $(SHARD): under the race detector"; fi; \
	echo "Shard $(SHARD): $$(echo $$PKGS | wc -w) package(s), tags: $$TAGS"; \
	go test -short -count=1 -timeout $(TEST_TIMEOUT) -tags "$$TAGS" "$$@" $$PKGS

# `Backend / Go build cache` in ci.yml compiles this once a day on main, and
# every other Go job starts from what it compiled. On a runner's two
# processors compiling is most of a Go job, so a job that finds its packages
# built spends its time on the tests. It builds the test binaries of the unit,
# minimal and untagged builds, the sdk's and every module's under the race
# detector as the sdk job runs them, the registry probes under every
# shipping tag set, and the export data internal/arch's session-assertion
# gate type-checks against, and runs no test.
go-cache-warm: ## (go) compile what the CI Go jobs compile, running no test
	@echo "1. the unit tag set..."
	@go test -run '^$$' -tags "$(SHARD_TAGS)" ./... > /dev/null
	@echo "2. the minimal build..."
	@TAGS="minimal $$(go run ./cmd/detect-build-tags --config "$(CONFIG_FILE)" --variant minimal --extra "$(EXTRA_BUILD_TAGS)")"; CGO_ENABLED=0 go test -run '^$$' -tags "$$TAGS" ./... > /dev/null
	@echo "3. no tags..."
	@go test -run '^$$' ./... > /dev/null
	@echo "4. the sdk and the modules under packages/, under the race detector..."
	@cd sdk && go test -race -run '^$$' ./... > /dev/null
	@for mod in packages/*/go.mod; do (cd "$${mod%/go.mod}" && go test -race -run '^$$' ./... > /dev/null) || exit 1; done
	@echo "5. the registry probes under every shipping tag set..."
	@go test -count=1 ./cmd/generate-error-codes/ ./cmd/generate-extensions/ > /dev/null
	@echo "6. the export data of the session-assertion gate..."
	@CGO_ENABLED=0 go list -export -deps -tags "full $(FULL_TAGS)" ./internal/... ./adapters/... ./cmd/... > /dev/null
	@echo "OK    the build cache holds what the Go jobs compile"

##@ Gates, what CI runs

# ci-local runs every CI job that can run on this machine, fastest first, and
# then lists what it could not run and why. Run it before every push. The
# commit checks take a range, for example `make ci-local RANGE=origin/main..HEAD`;
# without it they use `@{upstream}..HEAD`. It runs several lanes at once;
# `make ci-local CI_LOCAL_ARGS=-j1` runs one at a time.
CI_LOCAL_ARGS ?=

ci-local: ## (docker, node, pnpm, govulncheck where a lane needs one) every CI job that runs here, and what it skipped
	@bash scripts/ci-local.sh $(CI_LOCAL_ARGS)

ci-local-fast: ## (node, pnpm where a lane needs one) the lanes marked fast: generated files, commit gates, lint, unit tests
	@bash scripts/ci-local.sh --fast $(CI_LOCAL_ARGS)

ci-local-list: ## print what ci-local would run here, what it would not and why, running nothing
	@bash scripts/ci-local.sh --list $(CI_LOCAL_ARGS)

pre-commit: ## (go, pnpm) build, the frontend gates, lint and the unit tests, before a commit
	@echo "1. building..."
	@"$(MAKE)" build
	@echo "2. frontend gates..."
	@"$(MAKE)" frontend-check
	@echo "3. lint..."
	@"$(MAKE)" lint
	@echo "4. unit tests..."
	@"$(MAKE)" test-unit
	@echo "OK    every pre-commit check passed"

# lint and lint-fix share this one command. It installs the pinned version of
# golangci-lint when it is missing, then lints every Go module in the tree. A
# new Go module must be added to the list below, or nothing lints it. The root
# module is linted twice, the second time under the printer build's tags,
# because files behind those tags are compiled in no other run.
# extensions/extension.mk reads the version from the line below with sed, so
# keep it in the form `GOLANGCI_VERSION = X.Y.Z`.
GOLANGCI_VERSION = 2.14.0
LINT_FLAGS =

LINT_RUN = REQUIRED_VERSION="$(GOLANGCI_VERSION)"; \
  GOLANGCI_LINT=""; \
  if command -v golangci-lint > /dev/null 2>&1; then \
    CURRENT_VERSION=$$(golangci-lint version 2>&1 | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1); \
    if [ "$$CURRENT_VERSION" = "$$REQUIRED_VERSION" ]; then \
      GOLANGCI_LINT="golangci-lint"; \
    fi; \
  fi; \
  if [ -z "$$GOLANGCI_LINT" ]; then \
    GOPATH_BIN=$$(go env GOPATH)/bin/golangci-lint; \
    if [ -f "$$GOPATH_BIN" ]; then \
      CURRENT_VERSION=$$($$GOPATH_BIN version 2>&1 | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1); \
      if [ "$$CURRENT_VERSION" = "$$REQUIRED_VERSION" ]; then \
        GOLANGCI_LINT="$$GOPATH_BIN"; \
      fi; \
    fi; \
  fi; \
  if [ -z "$$GOLANGCI_LINT" ]; then \
    echo "Installing golangci-lint v$$REQUIRED_VERSION..."; \
    go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v$$REQUIRED_VERSION; \
    GOLANGCI_LINT=$$(go env GOPATH)/bin/golangci-lint; \
  fi; \
  RC=0; \
  $$GOLANGCI_LINT run $(LINT_FLAGS) ./... || RC=1; \
  echo "Running the linters again under the tags of the printer build..."; \
  $$GOLANGCI_LINT run $(LINT_FLAGS) --build-tags "$$(echo full printer $(PRINTER_TAGS) | tr ' ' ',')" ./... || RC=1; \
  ( cd sdk && $$GOLANGCI_LINT run $(LINT_FLAGS) ./... ) || RC=1; \
  ( cd packages/graphql && $$GOLANGCI_LINT run $(LINT_FLAGS) ./... ) || RC=1; \
  ( cd packages/fractal && $$GOLANGCI_LINT run $(LINT_FLAGS) ./... ) || RC=1; \
  exit $$RC

lint: ## (go) golangci-lint over every Go module
	@echo "Running the linters..."
	@$(LINT_RUN)

lint-fix: LINT_FLAGS = --fix
lint-fix: ## (go) golangci-lint over every Go module, fixing what it can
	@echo "Running the linters, fixing what they can..."
	@$(LINT_RUN)
	@echo "OK    linting finished"

style-check: ## the writing rules, over the documents, the comments and this file
	@echo "Writing style gates"
	@bash scripts/check-writing-style.sh

# The tests under internal/arch read the whole tree: the documents, the
# workflows, this file and the code. CI runs them on every change, in the job
# that skips for no path.
arch-check: ## (go) the architecture tests, over the whole tree
	@echo "Architecture gates"
	@go test -count=1 ./internal/arch/

# actionlint reads every workflow as GitHub will. shellcheck and pyflakes stay
# off, so the answer does not depend on what else is installed.
ACTIONLINT_VERSION := v1.7.12

workflows-check: ## (go) every workflow under .github/workflows passes actionlint
	@$(REQUIRE_GO)
	@echo "Workflow gates"
	@go run github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION) -shellcheck= -pyflakes=

# gitleaks runs through `go run`, so the network is needed the first time the
# pinned version is fetched.
secrets-check: ## (go) no credential in the files git tracks or would add
	@$(REQUIRE_GO)
	@echo "Secret gates"
	@bash scripts/check-secrets.sh

# A target missing from .PHONY stops working as soon as a file with that name
# exists, so this check also compares .PHONY with the targets.
makefile-check: ## every Makefile obeys the shell and .PHONY rules
	@echo "Makefile gates"
	@bash scripts/check-makefile.sh

changelog-check: ## every commit that needs a changelog entry has one, and every fragment in docs/changes is one a release can file
	@echo "Changelog gates"
	@bash scripts/check-changelog.sh $(RANGE)

# A change a user observes is a fragment in docs/changes/ (ADR-20261021-arm), named by
# the time it was written so two pull requests never pick one name.
NAME ?=
CATEGORY ?= Fixed
changelog-new: ## write a fragment of pending news in docs/changes: NAME=<words> [CATEGORY=Added]
	@sh scripts/new-change.sh "$(NAME)" "$(CATEGORY)"

changelog-preview: ## print the changelog section the next release would file, and change nothing
	@PREVIEW=1 sh scripts/cut-release-changelog.sh "$$(cat VERSION)-next"

# The commit-msg hook runs the same script on each new message. This target
# checks a whole range of commits at once.
commit-style-check: ## every commit message obeys the subject, body and trailer rules
	@echo "Commit message gates"
	@bash scripts/check-commit-style.sh $(RANGE)

# A decision record is named by the
# date and three random characters, so two pull requests never take one name.
TITLE ?=

adr-new: ## write a new decision record in docs/decisions with a fresh id: TITLE=<words>
	@sh scripts/new-record.sh adr "$(TITLE)"

# A migration is named by the date and time it was written (ADR-20261027-kqd),
# so two pull requests never take one name. This writes the file for both
# engines with one name.
migration-new: ## write a dated migration for both engines: DIR=<package directory> NAME=<words>
	@sh scripts/new-migration.sh "$(DIR)" "$(NAME)"

# The pre-push hook runs this same script first.
hooks-check: ## the installed hooks are this repository's, and a commit is checked
	@echo "Hook gates"
	@bash scripts/check-hooks.sh

# This needs no network. CI also refreshes scripts/go-releases.tsv from go.dev
# with `--refresh`, which this target does not.
go-version-check: ## the Go toolchain version agrees everywhere and is the newest release five days old
	@echo "Go toolchain gates"
	@bash scripts/check-go-version.sh

# govulncheck reports only vulnerabilities that this code can actually reach.
# It needs the network to download the vulnerability database, so the pre-push
# hook does not run it.
vuln-check: ## (go, network) no vulnerability reachable from either module's own symbols
	@echo "Vulnerability gates"
	@command -v govulncheck >/dev/null 2>&1 || { echo "FAIL  govulncheck is not on PATH. Install it with: go install golang.org/x/vuln/cmd/govulncheck@latest"; exit 1; }
	@echo "->    root module"
	@govulncheck ./...
	@echo "->    sdk module"
	@cd sdk && govulncheck ./...
	@echo "OK    no reachable vulnerability in either module"

# CI builds only for the machine it runs on, so this is what catches a build
# that breaks on arm64 or grows past the size budget.
binary-check: ## (go) every shipping binary cross-compiles, links no cgo and is inside its size budget
	@echo "Binary budget gates"
	@bash scripts/check-binary-budget.sh

# This takes about half a minute, because it analyses the whole program. The
# tool is pinned by version here and not in go.mod, so that it does not add
# its own dependencies to go.sum.
DEADCODE_VERSION := v0.49.0

deadcode-check: ## (go) no function under internal/ or adapters/ that nothing reaches
	@DEADCODE_TAGS="full $(FULL_TAGS)" DEADCODE_VERSION="$(DEADCODE_VERSION)" \
		bash scripts/check-reachability.sh

# Inside this repository the sdk is used through a local path. These checks
# compile it the way someone outside does, through its published import path:
# the sdk must import only the standard library, a separate module must compile
# against it, and every module under packages/ must build in its own folder.
sdk-check: ## (go) the sdk compiles the way an author outside this repository compiles it
	@echo "Checking that the sdk compiles for someone outside this repository..."
	@cd sdk && go mod download
	@go test -count=1 ./internal/arch/ \
		-run 'TestTheSdkImportsOnlyTheStandardLibrary|TestAnOutsideModuleCompilesAgainstTheSdk' -v
	@"$(MAKE)" side-modules-check
	@echo "OK    the sdk checks passed"

# `go build ./...` from the root skips the modules under packages/, because
# each has its own go.mod. This builds and vets each one from inside its folder.
side-modules-check: ## (go, network on a cold cache) every module beside the core builds and vets in its own folder
	@bash scripts/check-side-modules.sh

# This compares the OpenAPI document with the one the last release published,
# and refuses any change that tools/apicompat/policy.json does not allow.
api-check: ## (go) the API surface changed only in ways the compatibility policy permits
	@echo "API compatibility gates"
	@go test -count=1 ./tools/apicompat/
	@go run ./tools/apicompat check

# The baseline is a committed file, so a reviewer sees the accepted change in
# the diff.
api-accept: ## (go) accept the current API surface as the new compatibility baseline
	@go run ./tools/apicompat accept

# Every vocabulary file carries a version. This checks that the version moved
# when the content did: a new entry needs a minor version, a removed entry a
# major one. Step 2 runs two named tests and counts them, because `go test -run`
# with a name that no longer exists would pass silently.
VOCABULARY_INDEX_TESTS := TestEveryVocabularyRowWithNoVersionIsNamedInTheBaseline|TestEveryVersionedVocabularyRowHasABaselinedArtefact
VOCABULARY_INDEX_CASES := 2

unexport VOCABULARY_INDEX_TESTS
unexport VOCABULARY_INDEX_CASES

vocab-check: ## (go) every vocabulary artefact carries the version its set demands
	@echo "Vocabulary version gates"
	@echo "1. every vocabulary file carries the version its content needs..."
	@go test -count=1 ./tools/vocabcompat/
	@echo "2. the served index and the baseline name the same vocabularies..."
	@OUT=$$(go test -count=1 -v ./internal/transport/http/httpapi/ -run '$(VOCABULARY_INDEX_TESTS)' 2>&1); \
	echo "$$OUT" | grep -E '^(---|ok|FAIL|panic)'; \
	RAN=$$(echo "$$OUT" | grep -cE '^--- PASS: Test'); \
	test "$$RAN" -eq $(VOCABULARY_INDEX_CASES) || \
		{ echo "FAIL  step 2 ran $$RAN of $(VOCABULARY_INDEX_CASES) tests. A test was renamed or removed"; exit 1; }; \
	echo "   $$RAN of $(VOCABULARY_INDEX_CASES) tests ran"
	@echo "3. the committed files against the baseline..."
	@go run ./tools/vocabcompat check
	@echo "OK    vocabulary version gates passed"

vocab-accept: ## (go) accept the current vocabularies as the new version baseline
	@go run ./tools/vocabcompat accept

##@ Generated files

# Each generate-* target rewrites a committed file from the Go code, and the
# matching *-check target fails when the committed file is out of date. Never
# edit these files by hand.

ERROR_CODES_REGISTRY := docs/errors/error-codes.json
ERROR_CODES_REFERENCE := docs/src/content/docs/error-codes.md

generate-error-codes: ## (go) rewrite the error code registry and its reference page
	@echo "Generating the error code registry and reference..."
	@go run ./cmd/generate-error-codes

# A code that disappears would break a released client, so step 3 refuses
# that.
error-codes-check: ## (go) every error code is registered, documented and still there
	@echo "Error code gates"
	@echo "1. returned codes are registered..."
	@cd sdk && go test ./fault/...
	@go test -count=1 ./internal/transport/http/httpapi/
	@echo "2. every registered code is documented..."
	@go test -count=1 ./cmd/generate-error-codes/
	@echo "3. no code disappeared, 4. the committed files are up to date..."
	@go run ./cmd/generate-error-codes -check
	@go run ./cmd/generate-error-codes
	@git diff --exit-code -- $(ERROR_CODES_REGISTRY) $(ERROR_CODES_REFERENCE) || { \
		echo "FAIL  the error code files are out of date. Commit the regenerated files"; \
		exit 1; \
	}
	@echo "OK    error code gates passed"

EXTENSIONS_CATALOGUE := docs/extensions/extensions.json
EXTENSIONS_REFERENCE := docs/src/content/docs/extensions.md

generate-extensions: ## (go) rewrite the extension catalogue and its reference page
	@echo "Generating the extension catalogue and reference..."
	@go run ./cmd/generate-extensions

# An extension that disappears would break an operator's configuration, so
# step 3 refuses that.
extensions-check: ## (go) every extension is described, published and still there
	@echo "Extension registry gates"
	@echo "1. every extension has a description..."
	@cd sdk && go test ./extension/... ./connectorapi/... ./routesapi/...
	@go test -count=1 ./internal/extension/registry/
	@go test -count=1 ./internal/arch/ -run TestOnlyLegacyRegistersWithoutADescriptor
	@echo "2. every registered extension is documented..."
	@go test -count=1 ./cmd/generate-extensions/
	@echo "3. no extension disappeared, 4. the committed files are up to date..."
	@go run ./cmd/generate-extensions -check
	@go run ./cmd/generate-extensions
	@git diff --exit-code -- $(EXTENSIONS_CATALOGUE) $(EXTENSIONS_REFERENCE) || { \
		echo "FAIL  the extension files are out of date. Commit the regenerated files"; \
		exit 1; \
	}
	@echo "OK    extension registry gates passed"

CAPABILITY_REFERENCE := docs/src/content/docs/capabilities.md

generate-capabilities: ## (go) rewrite the capability reference page from the shipped profiles
	@echo "Generating the capability vocabulary reference..."
	@go run ./cmd/generate-capabilities

capabilities-check: ## (go) every capability key a profile declares is published
	@echo "Capability vocabulary gates"
	@echo "1. every declared key is published, 2. the page matches the profiles..."
	@go test -count=1 ./cmd/generate-capabilities/
	@go run ./cmd/generate-capabilities -check
	@echo "OK    capability vocabulary gates passed"

RESOURCE_KINDS_CATALOGUE := docs/vocabularies/resource-kinds.json
RESOURCE_KINDS_REFERENCE := docs/src/content/docs/resource-kinds.md

generate-resource-kinds: ## (go) rewrite the resource-kind catalogue and its reference page
	@echo "Generating the resource-kind set and reference..."
	@go run ./cmd/generate-resource-kinds

resource-kinds-check: ## (go) every resource kind is described and the artefacts match the table
	@echo "Resource kind vocabulary gates"
	@echo "1. every kind is described, 2. the code and the catalogue agree..."
	@go test -count=1 ./cmd/generate-resource-kinds/ ./internal/kernel/ref/
	@echo "3. the committed files are up to date..."
	@go run ./cmd/generate-resource-kinds -check
	@echo "OK    resource kind vocabulary gates passed"

AUTHZ_VOCABULARY_CATALOGUE := docs/vocabularies/authorization.json
AUTHZ_VOCABULARY_REFERENCE := docs/src/content/docs/authorization-vocabulary.md

generate-authz-vocabulary: ## (go) rewrite the authorization vocabulary and its reference page
	@echo "Generating the authorization vocabulary and reference..."
	@go run ./cmd/generate-authz-vocabulary

authz-vocabulary-check: ## (go) the authorization sets are described and the artefacts match them
	@echo "Authorization vocabulary gates"
	@echo "1. every action, type and relation is described and the code and the catalogue agree..."
	@go test -count=1 ./cmd/generate-authz-vocabulary/
	@go test -count=1 ./internal/authz/ -run "TestEveryAction|TestEveryType|TestEveryRelation|TestThe"
	@echo "2. the committed files are up to date..."
	@go run ./cmd/generate-authz-vocabulary -check
	@echo "OK    authorization vocabulary gates passed"

EVENT_TYPES_CATALOGUE := docs/vocabularies/event-types.json
EVENT_TYPES_REFERENCE := docs/src/content/docs/event-types.md

generate-event-types: ## (go) rewrite the event-type catalogue and its reference page
	@echo "Generating the event-type set and reference..."
	@go run ./cmd/generate-event-types

event-types-check: ## (go) every event type is described and no subscriber can be sent a forged one
	@echo "Event type vocabulary gates"
	@echo "1. every event type is described and the code and the catalogue agree..."
	@go test -count=1 ./cmd/generate-event-types/
	@echo "2. an unregistered event type never reaches a subscriber..."
	@cd sdk && go test -count=1 ./event/
	@go test -count=1 ./internal/transport/stream/ -run "TestPublishRefuses|TestPublishAccepts"
	@echo "3. the committed files are up to date..."
	@go run ./cmd/generate-event-types -check
	@echo "OK    event type vocabulary gates passed"

generate-slots: ## (go) rewrite every file that lists the module UI slots, then the OpenAPI document and the client SDK
	@echo "Generating the slot contract..."
	@go run ./cmd/generate-slots
	@"$(MAKE)" generate-openapi generate-client-sdk

slots-check: ## (go) the slot table is sound and every file that lists the slots matches it
	@echo "Slot table gates"
	@echo "1. the table and the generator hold their rules..."
	@cd sdk && go test -count=1 ./manifest/
	@go test -count=1 ./cmd/generate-slots/ ./internal/app/scaffold/
	@echo "2. the committed files are up to date..."
	@go run ./cmd/generate-slots -check
	@echo "OK    slot table gates passed"

DEVICE_ISSUES_CATALOGUE := docs/vocabularies/device-issues.json
DEVICE_ISSUES_REFERENCE := docs/src/content/docs/device-issues.md

generate-device-issues: ## (go) rewrite the device-issue catalogue and its reference page
	@echo "Generating the device-issue set and reference..."
	@go run ./cmd/generate-device-issues

# The nine tests below are named one by one and counted, because `go test -run`
# with a pattern that matches nothing would pass silently.
DEVICE_ISSUE_TESTS := TestEveryIssueTheDevicePlaneRaisesIsInTheClosedSet|TestAPrinterWaitingAtADialogNeedsAnOperator|TestADepartedPrinterCarriesOnlyTheDepartedIssue|TestTheIssueRollupCountsOnlyThePrintersTheCallerMaySee|TestTheIssueFilterNarrowsThePageAndNotTheRollup|TestTheFleetListingCostsNoStatementPerPrinter|TestACallerGrantedNothingLearnsNothingFromTheRollup|TestTheFleetListingReportsADialogAsAnIssueOverHTTP|TestAPrinterSpeakingAnUnreadableDialectIsReportedAsAnIssue
DEVICE_ISSUE_CASES := 9

# Keep this long list out of the environment of child processes.
unexport DEVICE_ISSUE_TESTS
unexport DEVICE_ISSUE_CASES

device-issues-check: ## (go) every device issue is described and every issue the device plane raises is in the set
	@echo "Device issue vocabulary gates"
	@echo "1. to 3. every reason is described and the artefact agrees both ways..."
	@go test -count=1 ./cmd/generate-device-issues/ ./internal/kernel/deviceissue/
	@echo "4. every issue the device plane raises is in the closed set..."
	@OUT=$$(go test -count=1 -v ./internal/app/ -run '$(DEVICE_ISSUE_TESTS)' 2>&1); \
	echo "$$OUT" | grep -E '^(---|ok|FAIL|panic)'; \
	RAN=$$(echo "$$OUT" | grep -cE '^--- PASS: Test'); \
	test "$$RAN" -eq $(DEVICE_ISSUE_CASES) || \
		{ echo "FAIL  gate 4 ran $$RAN of $(DEVICE_ISSUE_CASES) cases, so it passed over nothing"; exit 1; }; \
	echo "   $$RAN of $(DEVICE_ISSUE_CASES) cases ran"
	@echo "5. the artefacts match the table..."
	@go run ./cmd/generate-device-issues -check
	@echo "OK    device issue vocabulary gates passed"

# The device model catalogue is generated from the profiles under
# internal/printer/profile.
DEVICE_MODELS_CATALOGUE := docs/vocabularies/device-models.json
DEVICE_MODELS_REFERENCE := docs/src/content/docs/device-models.md

generate-device-models: ## (go) rewrite the device-model catalogue and its reference page
	@echo "Generating the device-model set and reference..."
	@go run ./cmd/generate-device-models

device-models-check: ## (go) every printer model is described and the artefacts match the catalogue
	@echo "Device model vocabulary gates"
	@echo "1. every model is described and the code and the catalogue agree..."
	@go test -count=1 ./cmd/generate-device-models/ ./internal/printer/profile/
	@echo "2. the committed files are up to date..."
	@go run ./cmd/generate-device-models -check
	@echo "OK    device model vocabulary gates passed"

# The texts are downloaded from the Prusa-Error-Codes commit the generated file
# records. COMMIT names a newer one, and COMMIT=latest is the newest commit of
# its master branch. The target needs the network, so no gate runs it. The
# generator's own test runs with the rest.
PRINTER_ERROR_CODES_REPO := https://github.com/prusa3d/Prusa-Error-Codes

generate-printer-error-codes: ## (go) rewrite the printer error code texts from Prusa-Error-Codes: [COMMIT=<full hash>|latest]
	@echo "Generating the printer error code texts..."
	@commit="$(COMMIT)"; \
	if [ "$$commit" = latest ]; then \
		commit=$$(git ls-remote $(PRINTER_ERROR_CODES_REPO) refs/heads/master | cut -f1); \
		[ -n "$$commit" ] || { echo "no commit read from $(PRINTER_ERROR_CODES_REPO)"; exit 1; }; \
		echo "the newest commit is $$commit"; \
	fi; \
	go run ./cmd/generate-printer-error-codes $${commit:+-commit "$$commit"}

# The wire protocol is what a module process and the server speak to each
# other. The test vectors are generated; the specification page is written by
# hand; the Go and the JavaScript implementations are checked against both.
WIRE_ARTEFACT := docs/wire/plugin-wire-protocol.json
WIRE_REFERENCE := docs/src/content/docs/plugin-wire-protocol.md
WIRE_EXAMPLE := examples/wire-protocol

generate-wire-protocol: ## (go) rewrite the plugin wire protocol artefact from the test vectors
	@echo "Generating the plugin wire protocol artefact..."
	@go run ./cmd/generate-wire-protocol

# Step 4 needs node. The JavaScript implementation was written from the
# specification page alone, which proves the page is enough to implement from.
wire-check: ## (go, node) the plugin wire protocol, both implementations, against the vectors
	@echo "Plugin wire protocol gates"
	@echo "1. the codec against the vectors..."
	@cd sdk && go test -count=1 ./wire/...
	@echo "2. the specification page matches the code..."
	@go test -count=1 ./internal/arch/ -run TestThePublishedWire
	@echo "3. the committed vectors are up to date..."
	@go run ./cmd/generate-wire-protocol -check
	@echo "4. the JavaScript implementation against the same vectors..."
	@cd $(WIRE_EXAMPLE) && node conformance.js
	@echo "5. a server and a module process speaking it..."
	@cd sdk && go test -count=1 ./wirerpc/
	@go test -count=1 ./internal/extension/wirehost/
	@echo "OK    plugin wire protocol gates passed"

# Run this before `make api-check`, so the compatibility check reads the new
# document. There is no separate generator program: the server builds the
# document when it starts, so the test that compares it has an -update flag
# that writes it out.
OPENAPI_PACKAGE := ./internal/transport/http/httpapi
OPENAPI_DOCUMENT := internal/transport/http/httpapi/openapi.json
OPENAPI_TEST := ^TestCommittedSpecMatchesTheCode$$
# The committed document is the full build's, and the diagnostics route is
# registered only behind its tag. A build without it serves that document less
# the one operation, which the untagged run of the package holds.
OPENAPI_TAGS := diagnostics

generate-openapi: ## (go) rewrite the committed OpenAPI document from the registered operations
	@echo "Generating the OpenAPI document..."
	@OUT="$$(go test -count=1 -tags "$(OPENAPI_TAGS)" -v -run '$(OPENAPI_TEST)' $(OPENAPI_PACKAGE) -update 2>&1)"; RC=$$?; \
	printf '%s\n' "$$OUT"; \
	if [ $$RC -ne 0 ]; then echo "FAIL  the generator exited $$RC"; exit $$RC; fi; \
	case "$$OUT" in \
	*"wrote openapi.json"*) ;; \
	*) echo "FAIL  nothing wrote $(OPENAPI_DOCUMENT). The test that writes it was renamed or removed"; exit 1;; \
	esac
	@echo "OK    $(OPENAPI_DOCUMENT)"

# The whole package runs and not one named test, because `go test -run` with a
# name that no longer exists would pass silently.
openapi-check: ## (go) the committed OpenAPI document is the one this build serves
	@echo "OpenAPI document gates"
	@echo "1. the committed document matches the registered operations..."
	@go test -count=1 -tags "$(OPENAPI_TAGS)" $(OPENAPI_PACKAGE)
	@go test -count=1 $(OPENAPI_PACKAGE)
	@echo "OK    openapi document gates passed"

##@ Documentation

# The documentation site is an Astro project under docs/.
docs: _ensure-pnpm ## (pnpm) build the documentation site into docs/dist
	@echo "Building the documentation site..."
	@if [ ! -d "docs" ]; then \
		echo "WARN  there is no docs directory, so nothing was built"; \
	else \
		cd docs && \
		echo "Installing the documentation dependencies..." && \
		pnpm install --frozen-lockfile && \
		echo "Building the site..." && \
		pnpm run build && \
		echo "OK    the documentation site is in docs/dist/"; \
	fi

install-docs-deps: _ensure-pnpm ## (pnpm) install the documentation site's dependencies alone
	@if [ -d "docs" ]; then \
		echo "Installing the documentation dependencies..."; \
		cd docs && pnpm install; \
		echo "OK    documentation dependencies installed"; \
	fi

# One page with broken frontmatter breaks the whole site, and only a build
# catches that.
docs-site-check: ## (node, docs/node_modules) build the documentation site and fail when it does not build
	@echo "Documentation site gates"
	@bash scripts/check-docs-site.sh

docs-links-check: ## (node) every internal documentation link resolves, for the site reader and the vault reader
	@echo "Documentation link gates"
	@bash scripts/check-docs-links.sh $(DOCS_LINKS_FLAGS)

docs-tables-check: ## (node) every table in the built site sits in a labelled, focusable scroll region
	@echo "Documentation table gates"
	@node docs/scripts/check-table-regions.mjs

docs-check: ## (node, docs/node_modules, go) build the documentation site, then check its links, its tables and the configuration reference
	@"$(MAKE)" docs-site-check
	@"$(MAKE)" docs-links-check
	@"$(MAKE)" docs-tables-check
	@"$(MAKE)" config-reference-check

# The configuration reference is generated from the Go structs the server
# reads its configuration into.
CONFIG_REFERENCE := docs/src/content/docs/config.md

generate-config-reference: ## (go) rewrite the configuration reference page from the structs
	@echo "Generating the configuration reference..."
	@go run ./cmd/generate-config-reference

config-reference-check: ## (go) every configuration key has a sentence and the page matches the structs
	@echo "Configuration reference gates"
	@echo "1. every key has a short description..."
	@go test -count=1 ./cmd/generate-config-reference/
	@echo "2. every configuration file in the tree loads..."
	@go test -count=1 ./internal/app/config/ -run 'TestEvery|TestTheSmallest'
	@echo "3. the committed page is up to date..."
	@go run ./cmd/generate-config-reference -check
	@echo "OK    configuration reference gates passed"

# Each reference page has a generated region between two marker comments. The
# API pages get theirs from the OpenAPI document, the Go pages from the doc
# comments of the sdk. The text outside the markers is written by hand and the
# generator never touches it.
SDK_REFERENCE_DIR := docs/src/content/docs/reference

generate-sdk-reference: ## (go) rewrite the generated region of every reference page under docs/src/content/docs/reference
	@echo "Generating the SDK reference regions..."
	@go run ./cmd/generate-sdk-reference

sdk-reference-check: ## (go) every reference page exists, names every operation, entry and exported identifier, and matches the sources
	@echo "SDK reference gates"
	@echo "1. every operation, entry and identifier is on its page..."
	@go test -count=1 ./cmd/generate-sdk-reference/
	@echo "2. the committed pages are up to date..."
	@go run ./cmd/generate-sdk-reference -check
	@echo "OK    SDK reference gates passed"

# This serves the documentation site the way it is deployed: built by
# docker/docs/Dockerfile and served by nginx under /docs. The port is
# DOCS_PORT in .env, 54322 by default.
docs-up: ## (docker) build the documentation site image and serve it, printing the URL
	@$(REQUIRE_DOCKER)
	@echo "Building and starting the documentation site..."
	@docker compose -f $(PGFIXTURE_COMPOSE) up -d --build --wait docs
	@PORT="$$(docker compose -f $(PGFIXTURE_COMPOSE) port docs 8080 | sed "s/.*://")"; \
	echo "OK    the documentation site is at http://127.0.0.1:$$PORT/docs/"

docs-down: ## (docker) stop and remove the documentation site container
	@docker compose -f $(PGFIXTURE_COMPOSE) rm -sfv docs

##@ The client packages and the web app

# The TypeScript client under packages/typescript is generated from the
# OpenAPI document and the vocabularies: openapi-typescript writes the types
# and cmd/generate-client-sdk writes the operations. Every output is committed.
CLIENT_SDK_DIR := packages/typescript
CLIENT_SDK_TYPES := $(CLIENT_SDK_DIR)/src/generated/core-api.ts

# The install runs every time. It is fast when nothing changed, and skipping it
# would leave a stale node_modules that pnpm then refuses to build with.
_client-sdk-deps: _ensure-pnpm
	@echo "Installing the client SDK dependencies..."
	@cd $(CLIENT_SDK_DIR) && pnpm install --frozen-lockfile

# The web app links this package through its dist/ folder, so it has to be
# built first. Type-checking reads src/ directly, so it works on a fresh
# checkout.
client-sdk-build: _client-sdk-deps ## (pnpm) build the client SDK into packages/typescript/dist
	@echo "Building the client SDK..."
	@cd $(CLIENT_SDK_DIR) && pnpm run build >/dev/null
	@echo "OK    the client SDK built"

# The example module under examples/service-plugin has its own generated
# client. It is regenerated here too, so it stays in step with the example's
# routes.
EXAMPLE_MODULE_DIR := examples/service-plugin
EXAMPLE_MODULE_NAMESPACE := com.github.aux.acme.example
EXAMPLE_MODULE_PACKAGE := example-service-client

# The web app reads the routes of the Fractal module through this client. The
# namespace is the one the module path in packages/fractal/go.mod gives.
FRACTAL_CLIENT_DIR := packages/fractal/client
FRACTAL_NAMESPACE := com.github.aux.prusa3d.fractal-link

generate-client-sdk: _client-sdk-deps ## (go, pnpm) rewrite the client SDK from the committed OpenAPI document and registries
	@echo "Generating the client SDK..."
	@cd $(CLIENT_SDK_DIR) && pnpm run generate:types >/dev/null
	@go run ./cmd/generate-client-sdk
	@go run ./cmd/generate-client-sdk -module \
		-document $(EXAMPLE_MODULE_DIR)/openapi.json \
		-namespace $(EXAMPLE_MODULE_NAMESPACE) \
		-name $(EXAMPLE_MODULE_PACKAGE) \
		-out $(EXAMPLE_MODULE_DIR)/client
	@go run ./cmd/generate-client-sdk -module \
		-document packages/fractal/openapi.json \
		-namespace $(FRACTAL_NAMESPACE) \
		-name fractal-client \
		-out $(FRACTAL_CLIENT_DIR)
	@cd $(FRACTAL_CLIENT_DIR) && pnpm install --frozen-lockfile >/dev/null && pnpm run generate:types >/dev/null

fractal-client-build: client-sdk-build ## (pnpm) build the Fractal module's client into packages/fractal/client/dist
	@cd $(FRACTAL_CLIENT_DIR) && pnpm install --frozen-lockfile >/dev/null && pnpm run build >/dev/null

client-sdk-check: _client-sdk-deps ## (go, pnpm) the client SDK is what the committed contracts generate, every entry is described, and its tests pass
	@echo "Client SDK gates"
	@echo "1. every operation, error code and event type is in the package and described..."
	@go test -count=1 ./cmd/generate-client-sdk/
	@echo "2. the committed files are up to date..."
	@go run ./cmd/generate-client-sdk -check
	@echo "3. the types file matches the OpenAPI document..."
	@git ls-files --error-unmatch $(CLIENT_SDK_TYPES) >/dev/null
	@cd $(CLIENT_SDK_DIR) && pnpm run generate:types >/dev/null
	@git diff --exit-code $(CLIENT_SDK_TYPES) \
		|| { echo "FAIL  the types file moved. Run make generate-client-sdk and commit it"; exit 1; }
	@echo "4. the package builds, every export is documented, and its tests pass..."
	@cd $(CLIENT_SDK_DIR) && pnpm run build >/dev/null && \
		{ OUT="$$(pnpm test 2>&1)" || { printf '%s\n' "$$OUT"; exit 1; }; }
	@echo "5. the Fractal module's client is what its fragment generates..."
	@go run ./cmd/generate-client-sdk -module -check \
		-document packages/fractal/openapi.json \
		-namespace $(FRACTAL_NAMESPACE) \
		-name fractal-client \
		-out $(FRACTAL_CLIENT_DIR)
	@cd $(FRACTAL_CLIENT_DIR) && pnpm install --frozen-lockfile >/dev/null && pnpm run generate:types >/dev/null
	@git diff --exit-code $(FRACTAL_CLIENT_DIR)/src/generated/ \
		|| { echo "FAIL  the Fractal client's types moved. Run make generate-client-sdk and commit them"; exit 1; }
	@echo "OK    client SDK gates passed"

# This publishes both client packages to a local registry and installs them
# into four small consumer projects, the way a user would. The Node consumer
# also runs against the demo. scripts/check-client-sdk-install.sh has the
# steps.
#
# The registry container is started for the run and removed afterwards.
NPM_FIXTURE_PORT ?= 54873

client-sdk-install-check: _client-sdk-deps _client-ui-deps ## (go, pnpm, docker, a Playwright Chromium) both client packages install from a registry into four consumers, and the Node one runs against the demo
	@echo "Client package install gates"
	@echo "Starting the registry..."
	@docker compose -f $(PGFIXTURE_COMPOSE) up -d --wait --no-deps npm-registry-fixture
	@NPM_FIXTURE_PORT=$(NPM_FIXTURE_PORT) bash scripts/check-client-sdk-install.sh ; RC=$$? ; \
		echo "Removing the registry..." ; \
		docker compose -f $(PGFIXTURE_COMPOSE) rm -sfv npm-registry-fixture >/dev/null ; \
		exit $$RC

# The component library is the Svelte package under packages/svelte.
CLIENT_UI_DIR := packages/svelte

_client-ui-deps: _ensure-pnpm
	@echo "Installing the component library dependencies..."
	@cd $(CLIENT_UI_DIR) && pnpm install --frozen-lockfile

client-ui-build: _client-ui-deps ## (pnpm) build the component library into packages/svelte/dist, declarations included
	@echo "Building the component library..."
	@cd $(CLIENT_UI_DIR) && pnpm run build >/dev/null
	@echo "OK    the component library built"

# The library's gates, one target each. CI runs them as three jobs side by
# side (the lint, the tests and the rest), because one after the other they
# took seven minutes on a two-core runner. client-ui-check runs all four here,
# one after the other.
client-ui-lint: _client-ui-deps ## (pnpm) the component library lints, type-aware, and is formatted
	@cd $(CLIENT_UI_DIR) && pnpm run lint && pnpm run format:check

client-ui-svelte-check: _client-ui-deps ## (pnpm) the component library type-checks with svelte-check
	@cd $(CLIENT_UI_DIR) && pnpm run check:svelte

# The browser tests need Chromium. Install it once with
# `pnpm exec playwright install chromium`.
client-ui-test: _client-ui-deps ## (pnpm, a Playwright Chromium) the component library's tests pass under Node and in a real browser
	@cd $(CLIENT_UI_DIR) && pnpm run test

client-ui-storybook: _client-ui-deps ## (pnpm) the component library's Storybook builds
	@cd $(CLIENT_UI_DIR) && pnpm run build-storybook >/dev/null

client-ui-check: _client-ui-deps ## (pnpm, a Playwright Chromium) the component library lints, type-checks, its tests pass under Node and in a real browser, and its Storybook builds
	@echo "Component library gates"
	@echo "1. lint and formatting..."
	@cd $(CLIENT_UI_DIR) && pnpm run lint && pnpm run format:check
	@echo "2. svelte-check..."
	@cd $(CLIENT_UI_DIR) && pnpm run check:svelte
	@echo "3. the tests, under Node and in Chromium..."
	@cd $(CLIENT_UI_DIR) && pnpm run test
	@echo "4. the Storybook..."
	@cd $(CLIENT_UI_DIR) && pnpm run build-storybook >/dev/null
	@echo "OK    component library gates passed"

# The web app is the Vite project under frontend/. It uses the component
# library and the client SDK from this checkout.
APP_DIR := frontend

# The Storybook config imports the design CLI, so svelte-check and the tests
# of the web app read tools/design/node_modules too.
_app-deps: _ensure-pnpm
	@echo "Installing the web app dependencies..."
	@cd $(APP_DIR) && pnpm install --frozen-lockfile
	@cd tools/design && pnpm install --frozen-lockfile

frontend-build: _app-deps client-sdk-build client-ui-build fractal-client-build ## (pnpm) build the web app into frontend/dist
	@echo "Building the web app..."
	@cd $(APP_DIR) && pnpm run build >/dev/null
	@echo "OK    the web app built"

# The stories of these extensions are drawn in the product Storybook and
# import their own dependencies from their own node_modules.
STORYBOOK_EXTENSIONS := threemf server-slicing simple-queue

# A Storybook that spreads this app's config reads the same, so it runs this
# target first.
frontend-storybook-deps: _app-deps client-sdk-build client-ui-build fractal-client-build fleet-stats-ui-build ## (pnpm) install and build what the Storybook of the web app reads
	@for EXT in $(STORYBOOK_EXTENSIONS); do (cd extensions/$$EXT/ui && pnpm install --frozen-lockfile) || exit 1; done

# The design status of each story file becomes its `status:` tag in the sidebar.
frontend-storybook: frontend-storybook-deps ## (pnpm) the Storybook of the web app, the component library and three extensions builds
	@node tools/design/src/cli.ts status --write frontend/.storybook/generated/design-status.json
	@cd $(APP_DIR) && pnpm run build-storybook >/dev/null

# tsc comes from the web app, whose node_modules also hold the Vite types that
# frontend/vite/import-map.ts imports into the CLI.
design-check: _app-deps ## (node, pnpm) the design CLI's tests pass and it type-checks
	@node --test 'tools/design/test/*.test.ts'
	@cd $(APP_DIR) && pnpm exec tsc -p ../tools/design/tsconfig.json
	@echo "OK    the design CLI's tests pass and it type-checks"

# Storybook serves this bundle as the installed extension of its module page.
fleet-stats-ui-build: client-sdk-build client-ui-build ## (pnpm) build the fleet-stats extension's bundle into extensions/fleet-stats/ui/dist
	@cd extensions/fleet-stats/ui && pnpm install --frozen-lockfile && pnpm run build >/dev/null

# Step 4 measures every route in a real browser: layout shift, text contrast
# and click response. Step 5 counts the visual elements of every route
# (radii, text sizes, colours, and so on) against frontend/test/craft/baseline.json,
# so a new one fails until someone records it with `pnpm run test:craft:record`.
# Step 6 refuses a raw search box in any listing (scripts/check-listing-search.sh).
# Step 7 runs the shared lint preset's own tests, which prove each consumer
# config refuses a raw control and an exception that excuses nothing.
#
# Every step runs even when one before it fails, and the last line names each
# step that failed. A red lint must not hide a red UX measure behind it.
frontend-check: _app-deps client-sdk-build client-ui-build fractal-client-build ## (pnpm, a Playwright Chromium) the web app lints, type-checks, and its tests pass under Node and in a real browser
	@echo "Web app gates"; \
	FAILED=""; \
	echo "1. lint and formatting..."; \
	( cd $(APP_DIR) && pnpm run lint && pnpm run format:check ) || FAILED="$$FAILED lint"; \
	echo "2. svelte-check..."; \
	( cd $(APP_DIR) && pnpm run check:svelte ) || FAILED="$$FAILED svelte-check"; \
	echo "3. the tests, under Node and in Chromium..."; \
	( cd $(APP_DIR) && pnpm run test ) || FAILED="$$FAILED tests"; \
	echo "4. the UX measures, in Chromium over a served app..."; \
	( cd $(APP_DIR) && pnpm run test:ux ) || FAILED="$$FAILED ux"; \
	echo "5. the craft counts, in Chromium over a served app..."; \
	( cd $(APP_DIR) && pnpm run test:craft ) || FAILED="$$FAILED craft"; \
	echo "6. no raw search box outside ListingSearch..."; \
	bash scripts/check-listing-search.sh || FAILED="$$FAILED listing-search"; \
	echo "7. the tests of the shared lint preset..."; \
	( cd packages/standards && pnpm install --frozen-lockfile >/dev/null && pnpm run test ) || FAILED="$$FAILED standards"; \
	if [ -n "$$FAILED" ]; then \
		echo "FAIL  web app gates:$$FAILED"; \
		exit 1; \
	fi; \
	echo "OK    web app gates passed"

# Go can only embed files inside the package that declares them, so the built
# app is copied from frontend/dist into that package before `go build`. A
# committed placeholder keeps the folder there, so the build works in a
# checkout where nobody has built the app.
#
# cmd/compress-app then replaces each text file with a gzip copy. The server
# sends that copy to a browser as it is stored, so the binary is smaller and no
# request spends processor time on compression.
APP_EMBED_DIR := internal/transport/http/httpapi/appdist

embed-app: frontend-build ## (go, pnpm) build the web app, compress it and copy it where the full build embeds it
	@$(REQUIRE_GO)
	@echo "Embedding the web app from $(APP_DIR)/dist..."
	@if [ ! -f "$(APP_DIR)/dist/index.html" ]; then \
		echo "FAIL  $(APP_DIR)/dist/index.html is missing, so there is no app to embed."; \
		exit 1; \
	fi
	@rm -rf $(APP_EMBED_DIR)
	@mkdir -p $(APP_EMBED_DIR)
	@cp -r $(APP_DIR)/dist/. $(APP_EMBED_DIR)/
	@go run ./cmd/compress-app $(APP_EMBED_DIR)
	@touch $(APP_EMBED_DIR)/.gitkeep
	@echo "OK    the web app is in $(APP_EMBED_DIR)/"

# This is the only check that builds the binary with the app inside and opens
# the address a visitor opens.
embedded-app-check: embed-app ## (go, pnpm) the full-tag binary carries the web app and serves it at /
	@sh scripts/check-embedded-app.sh

clean-app: ## delete the embedded web app, keeping the placeholder the build needs
	@rm -rf $(APP_EMBED_DIR)
	@mkdir -p $(APP_EMBED_DIR)
	@touch $(APP_EMBED_DIR)/.gitkeep
	@echo "OK    the embedded app is removed"

# While this repository is private, nothing is published to npmjs. This local
# registry is how a developer installs the three client packages into another
# project. It is separate from the one client-sdk-install-check starts: this one
# is on port 54874 and keeps what is published in a named volume.
NPM_REGISTRY_PORT ?= 54874
NPM_REGISTRY_URL := http://127.0.0.1:$(NPM_REGISTRY_PORT)/
NPM_REGISTRY_VOLUME := prusa_connect_npm_registry_storage

# The version to publish. When empty, it is the tree's VERSION with
# `-dev.<short sha>` after it, so every commit gets its own version.
VERSION_TAG ?=

registry-up: ## (docker) start the local npm registry and keep what it holds
	@docker compose -f $(PGFIXTURE_COMPOSE) up -d --wait --no-deps npm-registry
	@echo "OK    the local npm registry is at $(NPM_REGISTRY_URL)"

# The volume with the published versions stays.
registry-down: ## (docker) stop and remove the local npm registry, keeping the versions it holds
	@docker compose -f $(PGFIXTURE_COMPOSE) rm -sf npm-registry

# This is the one command that deletes the published versions.
registry-reset: ## (docker) remove the local npm registry and every version it holds
	@docker compose -f $(PGFIXTURE_COMPOSE) rm -sf npm-registry && docker volume rm -f $(NPM_REGISTRY_VOLUME) > /dev/null && echo "OK    the local npm registry holds nothing"

registry-publish: _client-sdk-deps _client-ui-deps ## (pnpm, docker) build the three client packages and publish them into the local registry, at VERSION_TAG or VERSION-dev.<sha>
	@bash scripts/registry-publish.sh --registry $(NPM_REGISTRY_URL) --version "$(VERSION_TAG)" --dist-tag dev --token local

registry-status: ## (docker) print every version of the three client packages the local registry holds
	@bash scripts/registry-status.sh $(NPM_REGISTRY_URL)

# Without DIR this prints the .npmrc lines. With DIR=<path> it writes them
# into that project. The registry needs no password; it only listens on this
# machine.
registry-use: ## (node) print the .npmrc lines a project needs to install from the local registry, or write them into DIR=<path>
	@bash scripts/registry-use.sh $(NPM_REGISTRY_URL) $(DIR)

##@ Modules and examples

# This writes a new printer driver that compiles, with its descriptor and its
# conformance test. NAME and MODULE are required. DIR defaults to the last part
# of MODULE. For example:
#
#   make scaffold-driver NAME=acme MODULE=example.com/prusa-driver-acme
SCAFFOLD_DIR ?= ../$(NAME)

scaffold-driver: ## (go) write a new printer driver, its descriptor and its conformance test
	@test -n "$(NAME)" || { echo "FAIL  NAME is required: make scaffold-driver NAME=acme MODULE=..."; exit 1; }
	@test -n "$(MODULE)" || { echo "FAIL  MODULE is required: make scaffold-driver NAME=acme MODULE=..."; exit 1; }
	@go run ./cmd/scaffold-driver -name $(NAME) -module $(MODULE) -dir $(SCAFFOLD_DIR)

# This writes a new module with cmd/new-module. On a terminal it asks for
# whatever is not given. NAME and REPO are passed as -name and -repo, and
# ARGS carries any other flag. For example:
#
#   make new-module
#   make new-module NAME=spools REPO=github.com/acme/spools
new-module: ## (go) write a new module: UI only, process only, or both
	@go run ./cmd/new-module $(if $(NAME),-name $(NAME)) $(if $(REPO),-repo $(REPO)) $(ARGS)

# This builds the two example modules, installs them with `module add`, starts
# the demo and drives both through the API.
example-spools-check: ## (go, pnpm, curl, a Playwright Chromium) both proof extensions build, install with `module add` and answer through the API
	bash scripts/check-example-spools.sh

##@ Containers

# Run this after a change under docker/server/ or to either compose file.
docker-check: ## (docker) the runtime image runs as a non-root user and the hardened stack reaches healthy
	@$(REQUIRE_DOCKER)
	@echo "Building the runtime image..."
	@docker build -f docker/server/Dockerfile --build-arg BUILD_VERSION="$(BUILD_VERSION)" --build-arg UPGRADE_PUBLIC_KEYS="$(UPGRADE_PUBLIC_KEYS)" -t prusa-connect:local .
	@echo "Building the test images..."
	@docker compose -f docker-compose.local.yml build mosquitto-fixture mock-oidc npm-registry-fixture keycloak-fixture authentik-fixture authelia-fixture race
	@echo "Checking that the image runs as a non-root user and can write its mounts..."
	@bash scripts/check-container-nonroot.sh prusa-connect:local
	@echo "Checking that the server answers on every published port..."
	@bash scripts/check-container-probes.sh prusa-connect:local
	@echo "Checking that a port below 1024 can be bound without extra capabilities..."
	@bash scripts/check-container-privileged-port.sh prusa-connect:local
	@echo "Waiting for the hardened stack to become healthy..."
	@PRUSA_CONNECT_TAG=local docker compose -f docker-compose.local.yml up -d --no-build
	@for i in $$(seq 1 30); do \
		STATUS=$$(docker inspect --format '{{.State.Health.Status}}' prusa_connect 2>/dev/null || echo missing); \
		echo "  attempt $$i: $$STATUS"; \
		if [ "$$STATUS" = "healthy" ]; then break; fi; \
		if [ "$$STATUS" = "unhealthy" ]; then break; fi; \
		sleep 5; \
	done; \
	STATUS=$$(docker inspect --format '{{.State.Health.Status}}' prusa_connect 2>/dev/null || echo missing); \
	docker compose -f docker-compose.local.yml down -v > /dev/null 2>&1; \
	if [ "$$STATUS" != "healthy" ]; then echo "FAIL  the stack did not reach healthy ($$STATUS)"; exit 1; fi
	@echo "OK    the image is hardened and the stack is healthy"

# This removes only what belongs to this project. Everything compose creates
# carries the project label, so another project's containers are never
# touched. The two Go cache volumes are labelled to keep, so they are listed
# and not removed.
docker-clean: ## (docker) remove this project's stopped containers, dangling images, unused networks and volumes
	@$(REQUIRE_DOCKER)
	@echo "Stopped containers of project $(COMPOSE_PROJECT)"
	@docker container prune -f --filter "label=com.docker.compose.project=$(COMPOSE_PROJECT)"
	@echo "Dangling images of project $(COMPOSE_PROJECT)"
	@docker image prune -f --filter "label=com.docker.compose.project=$(COMPOSE_PROJECT)"
	@echo "Unused networks of project $(COMPOSE_PROJECT)"
	@docker network prune -f --filter "label=com.docker.compose.project=$(COMPOSE_PROJECT)"
	@echo "Unused volumes of project $(COMPOSE_PROJECT), keeping the caches"
	@for v in $$(docker volume ls -q --filter dangling=true --filter "label=com.docker.compose.project=$(COMPOSE_PROJECT)"); do \
		if [ -n "$$(docker volume inspect --format '{{index .Labels "com.prusa3d.keep"}}' "$$v")" ]; then \
			echo "kept $$v"; \
		else \
			docker volume rm "$$v" > /dev/null && echo "removed $$v"; \
		fi; \
	done
	@echo "OK    project $(COMPOSE_PROJECT) is clean"

# The Fractal mockup runs four real servers in one tree: hq at the top, brno
# under it, workshop under brno with fake printers, and garage under hq with
# fake printers. Each one is this binary with the Fractal module installed.
# The ports are set in .env, 18080 to 18083 by default, on this machine only.
PCL_MOCKUP_HQ_PORT ?= 18080
PCL_MOCKUP_BRNO_PORT ?= 18081
PCL_MOCKUP_WORKSHOP_PORT ?= 18082
PCL_MOCKUP_GARAGE_PORT ?= 18083
MOCKUP_SERVICES := hq brno workshop garage

# Compose adds the project name in front of a volume name, so these are the
# real names.
MOCKUP_VOLUMES = $(COMPOSE_PROJECT)_pcl_mockup_hq_data $(COMPOSE_PROJECT)_pcl_mockup_brno_data $(COMPOSE_PROJECT)_pcl_mockup_workshop_data $(COMPOSE_PROJECT)_pcl_mockup_garage_data

mockup-up: ## (docker) start the four-server Fractal mockup, with fake printers on two of them
	@$(REQUIRE_DOCKER)
	@echo "Building and starting the Fractal mockup..."
	@docker compose -f $(PGFIXTURE_COMPOSE) --profile mockup up -d --build --wait $(MOCKUP_SERVICES)
	@echo "OK    hq serves the app at http://127.0.0.1:$(PCL_MOCKUP_HQ_PORT), brno at http://127.0.0.1:$(PCL_MOCKUP_BRNO_PORT),"
	@echo "      workshop at http://127.0.0.1:$(PCL_MOCKUP_WORKSHOP_PORT) and garage at http://127.0.0.1:$(PCL_MOCKUP_GARAGE_PORT)"
	@echo "      Run make mockup-check to test the four servers, then make mockup-seed to click through Storage."

mockup-status: ## (docker) print the links and nodes every mockup server reports, through the API
	@$(REQUIRE_DOCKER)
	@COMPOSE=$(PGFIXTURE_COMPOSE) bash docker/mockup/status.sh

# After mockup-check, never before it: the check expects hq to hold brno and
# garage as pending, and the seed admits them.
mockup-seed: ## (docker) admit the mockup servers and give them folders to share and browse in Storage
	@$(REQUIRE_DOCKER)
	@COMPOSE=$(PGFIXTURE_COMPOSE) sh docker/mockup/seed.sh

mockup-check: ## (docker) drive the four mockup servers over their APIs and fail when a step does not happen
	@$(REQUIRE_DOCKER)
	@COMPOSE=$(PGFIXTURE_COMPOSE) bash docker/mockup/check.sh

mockup-down: ## (docker) stop the Fractal mockup and remove what it wrote, keeping the image
	@$(REQUIRE_DOCKER)
	@docker compose -f $(PGFIXTURE_COMPOSE) --profile mockup rm -sfv $(MOCKUP_SERVICES)
	@docker volume rm -f $(MOCKUP_VOLUMES) > /dev/null
	@docker network rm pcl_mockup > /dev/null 2>&1 || true
	@echo "OK    the Fractal mockup is removed. The image stays."

##@ Release

# Each of these raises the version, updates the files that carry it, commits
# and creates the tag. None of them pushes.
release-alpha: ## cut the next alpha (0.0.4-alpha.N; BUMP=minor|major starts 0.1.0 or 1.0.0), commit and tag, without pushing
	@bash scripts/release.sh alpha $(BUMP)

release-beta: ## cut the next beta (0.0.4-beta.N; BUMP=minor|major as for alpha), commit and tag, without pushing
	@bash scripts/release.sh beta $(BUMP)

release-stable: ## cut the stable release (0.0.4; BUMP=minor|major as for alpha), commit and tag, without pushing
	@bash scripts/release.sh stable $(BUMP)
