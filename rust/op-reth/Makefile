DEPRECATED_TARGETS := install build build-debug \
	build-x86_64-apple-darwin build-aarch64-apple-darwin \
	build-x86_64-unknown-linux-gnu build-aarch64-unknown-linux-gnu build-x86_64-pc-windows-gnu \
	build-release-tarballs \
	test-unit cov-unit cov-report-html \
	docker-build-push docker-build-push-git-sha docker-build-push-latest \
	docker-build-push-nightly docker-build-push-nightly-edge-profiling docker-build-push-nightly-profiling \
	clean profiling maxperf maxperf-no-asm \
	fmt clippy lint-typos lint-toml lint clippy-fix fix-lint \
	rustdocs cargo-test test-doc test-all pr \
	check-udeps test test-integration check-windows examples

include ../../justfiles/deprecated.mk

# Pattern rules need manual shims because the generic deprecated-target
# macro can't handle Make's build-% / build-native-% patterns.
# These forward to the parameterized just recipes.

# List of features to use when building. Can be overridden via the environment.
# No jemalloc on Windows
ifeq ($(OS),Windows_NT)
    FEATURES ?= asm-keccak min-debug-logs
else
    FEATURES ?= jemalloc asm-keccak min-debug-logs
endif

# Cargo profile for builds. Default is for local builds, CI uses an override.
PROFILE ?= release

# Extra flags for Cargo
CARGO_INSTALL_EXTRA_FLAGS ?=

# The docker image name
DOCKER_IMAGE_NAME ?= us-docker.pkg.dev/oplabs-tools-artifacts/images/op-reth

##@ Help

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "Usage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Build

.PHONY: install
install: ## Build and install the op-reth binary under `$(CARGO_HOME)/bin`.
	cargo install --path bin --bin op-reth --force --locked \
		--features "$(FEATURES)" \
		--profile "$(PROFILE)" \
		$(CARGO_INSTALL_EXTRA_FLAGS)

.PHONY: build
build: ## Build the op-reth binary into `target` directory.
	cargo build --bin op-reth --features "$(FEATURES)" --profile "$(PROFILE)"

.PHONY: build-debug
build-debug: ## Build the op-reth binary into `target/debug` directory.
	cargo build --bin op-reth --features "$(FEATURES)"

# Builds the op-reth binary natively.
build-native-%:
	@echo
	@printf '%s\n' '$(call banner-style,Deprecated make call: make build-native-$*)'
	@printf '%s\n' '$(call banner-style,Consider using just instead: just build-native $*)'
	@echo
	just build-native $*

.PHONY: build-%
build-%:
	@echo
	@printf '%s\n' '$(call banner-style,Deprecated make call: make build-$*)'
	@printf '%s\n' '$(call banner-style,Consider using just instead: just build-cross $*)'
	@echo
	just build-cross $*
