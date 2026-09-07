# DNSGuard — mọi thứ cần cho vòng lặp phát triển và đóng gói.
#
# Không có mục tiêu nào cần công cụ ngoài Go, Node và Docker: migration nhúng trong
# binary, giao diện nhúng trong binary, CSDL là một file.

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: help dev dev-api dev-web build build-arm64 test test-go test-web \
        lint fmt image seed-traffic clean run

help:            ## Liệt kê các mục tiêu
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
	  awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

dev:             ## Backend :8080 và frontend :5173, cả hai hot reload
	@$(MAKE) -j2 dev-api dev-web

dev-api:
	@command -v air >/dev/null 2>&1 && air -c .air.toml || go run ./cmd/dnsguard

dev-web:
	cd web && npm run dev

build:           ## Một binary có nhúng giao diện
	cd web && npm run build
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/dnsguard ./cmd/dnsguard
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/dnsguard-cli ./cmd/dnsguard-cli
	@echo "→ bin/dnsguard $(VERSION)"

build-arm64:     ## Binary cho máy ARM64
	cd web && npm run build
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" \
		-o bin/dnsguard-arm64 ./cmd/dnsguard
	@echo "→ bin/dnsguard-arm64 $(VERSION)"

run: build       ## Biên dịch rồi chạy với dữ liệu trong ./data
	DNSGUARD_DB_PATH=./data/dnsguard.db \
	DNSGUARD_LISTS_DIR=./data/lists \
	./bin/dnsguard


run-dev : build       ## Biên dịch rồi chạy với dữ liệu trong ./data
	DNSGUARD_DB_PATH=./data/dnsguard.db \
	DNSGUARD_LISTS_DIR=./data/lists \
	go run ./cmd/dnsguard

test: test-go test-web  ## Toàn bộ test

test-go:
	go test ./... -race -count=1

test-web:
	cd web && npm run typecheck

lint:            ## gofmt, go vet, staticcheck
	@test -z "$$(gofmt -l ./cmd ./internal)" || (gofmt -l ./cmd ./internal; exit 1)
	go vet ./...
	@command -v staticcheck >/dev/null 2>&1 && staticcheck ./... || \
		echo "bỏ qua staticcheck (chưa cài)"

fmt:
	gofmt -w ./cmd ./internal

image:           ## Ảnh Docker multi-arch
	docker buildx build --platform linux/amd64,linux/arm64 \
		--build-arg VERSION=$(VERSION) \
		-f deploy/Dockerfile -t dnsguard:$(VERSION) -t dnsguard:latest .

seed-traffic:    ## Gửi truy vấn DNS giả vào cổng TZSP để có dữ liệu làm việc
	go run ./tools/seed-traffic

clean:
	rm -rf bin internal/web/assets/assets internal/web/assets/index.html
