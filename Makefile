# Release builds for zap.
#
# `make bundle` produces one conforming bundle archive per platform, which is
# what install.sh downloads and what `zap install <release-url>` installs.
# Each archive holds <id>/app.toml and <id>/contents/bin/zap.

APP_ID := io.github.huyng.zap
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.0.0)
PLATFORMS ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64
DIST := dist

# The packer runs on the build host, so it lives apart from the per-platform
# trees and is rebuilt whenever the sources change.
PACKER := build/.zap
SOURCES := $(shell find . -name '*.go' -not -path './build/*' -not -path './dist/*')

.PHONY: all build test bundle checksums clean

all: build

build:
	go build -trimpath -o zap .

test:
	go vet ./...
	go test ./...

# Zap packs its own releases, so build a host binary to run first.
$(PACKER): $(SOURCES) go.mod | build
	go build -trimpath -o $(PACKER) .

build:
	@mkdir -p build

bundle: $(PACKER)
	@rm -rf $(DIST)/$(APP_ID)*
	@mkdir -p $(DIST)
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; \
		arch=$${platform#*/}; \
		root=build/$$os-$$arch/$(APP_ID); \
		echo "building $(APP_ID) for $$os/$$arch"; \
		rm -rf build/$$os-$$arch; \
		mkdir -p $$root/contents/bin; \
		GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "-s -w" \
			-o $$root/contents/bin/zap . || exit 1; \
		sed 's/^version = .*/version = "$(VERSION)"/' app.toml > $$root/app.toml; \
		$(PACKER) bundle $$root --out $(DIST) --os $$os --arch $$arch || exit 1; \
	done

# A checksum beside each archive, which install.sh and zap both read.
checksums:
	@cd $(DIST) && for archive in *.tar.gz; do \
		if command -v sha256sum >/dev/null 2>&1; then \
			sha256sum "$$archive" | sed 's| .*/| |' > "$$archive.sha256sum"; \
		else \
			shasum -a 256 "$$archive" | sed 's| .*/| |' > "$$archive.sha256sum"; \
		fi; \
	done
	@ls -1 $(DIST)/*.sha256sum

release: bundle checksums
	@echo
	@echo "upload these to a GitHub release:"
	@ls -1 $(DIST)/*.tar.gz $(DIST)/*.sha256sum

clean:
	rm -rf build $(DIST) zap
