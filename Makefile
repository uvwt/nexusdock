GO_SOURCES := $(shell find cmd internal tests -type f -name '*.go' -print | sort)
WEB_INSTALL_STAMP := web/node_modules/.install-stamp
# 构建期版本信息：version 取 git describe（无标签时回退提交号），revision 取完整提交号。
# 不在 git 仓库内构建时回退 dev/unknown，与 internal/buildinfo 的缺省值保持一致。
BUILD_VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_REVISION := $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
BUILD_SOURCE := unknown
BUILD_LDFLAGS := -X github.com/uvwt/nexusdock/internal/buildinfo.Version=$(BUILD_VERSION) \
	-X github.com/uvwt/nexusdock/internal/buildinfo.Revision=$(BUILD_REVISION) \
	-X github.com/uvwt/nexusdock/internal/buildinfo.Source=$(BUILD_SOURCE)

.PHONY: fmt fmt-check test test-race vet tidy-check contracts repository-check web-deps web-build build build-nexusdock check ci run run-nexusdock clean

fmt:
	gofmt -w $(GO_SOURCES)

fmt-check:
	@files="$$(gofmt -l $(GO_SOURCES))"; \
	if [ -n "$$files" ]; then \
		printf '以下 Go 文件需要 gofmt：\n%s\n' "$$files"; \
		exit 1; \
	fi

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

tidy-check:
	go mod tidy -diff

contracts:
	python3 scripts/test_check_contracts.py
	python3 scripts/check-contracts.py

repository-check:
	python3 scripts/check-repository.py

$(WEB_INSTALL_STAMP): web/package.json web/package-lock.json
	cd web && npm ci
	@touch $(WEB_INSTALL_STAMP)

web-deps: $(WEB_INSTALL_STAMP)

web-build: web-deps
	cd web && npm run build

check: fmt-check tidy-check test vet contracts repository-check

ci: web-build check test-race build-nexusdock

build: web-build check build-nexusdock

build-nexusdock:
	mkdir -p bin
	go build -trimpath -ldflags '$(BUILD_LDFLAGS)' -o bin/nexusdock ./cmd/nexusdock

run: run-nexusdock

run-nexusdock:
	go run ./cmd/nexusdock

clean:
	rm -rf bin web/node_modules web/.vite web/tsconfig.tsbuildinfo
