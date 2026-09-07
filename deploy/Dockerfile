# syntax=docker/dockerfile:1

# ---------- Giai đoạn 1: biên dịch giao diện ----------
FROM node:22-alpine AS web

WORKDIR /src/web

# Chép manifest trước rồi mới chép mã nguồn: lớp cài phụ thuộc chỉ dựng lại khi
# package.json đổi, không phải mỗi lần sửa một dòng TypeScript.
COPY web/package.json web/package-lock.json* ./
RUN npm ci --no-audit --no-fund

COPY web/ ./
RUN npx tsc --noEmit && npx vite build


# ---------- Giai đoạn 2: biên dịch backend ----------
FROM golang:1.26-alpine AS api

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ ./cmd/
COPY internal/ ./internal/

# Giao diện đã biên dịch phải có mặt trước khi biên dịch Go: gói internal/web nhúng
# thẳng thư mục này vào binary.
COPY --from=web /src/internal/web/assets/ ./internal/web/assets/

ARG VERSION=docker
ARG TARGETARCH

# CGO_ENABLED=0 cho binary tĩnh: driver SQLite ở đây là Go thuần, nên không cần
# libc trong image cuối và ảnh chạy được trên cả amd64 lẫn arm64.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/dnsguard ./cmd/dnsguard && \
    CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/dnsguard-cli ./cmd/dnsguard-cli


# ---------- Giai đoạn 3: ảnh chạy ----------
FROM alpine:3.21

# ca-certificates cho HTTPS khi đồng bộ blocklist; tzdata để log hiển thị đúng múi giờ.
RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 10001 -h /var/lib/dnsguard dnsguard

COPY --from=api /out/dnsguard /usr/local/bin/dnsguard
COPY --from=api /out/dnsguard-cli /usr/local/bin/dnsguard-cli

# Thư mục dữ liệu và thư mục danh sách tách riêng: danh sách phải còn phục vụ được
# ngay cả khi cần khôi phục CSDL từ bản sao lưu.
RUN mkdir -p /var/lib/dnsguard/lists && chown -R dnsguard:dnsguard /var/lib/dnsguard

USER dnsguard
WORKDIR /var/lib/dnsguard

ENV DNSGUARD_DB_PATH=/var/lib/dnsguard/dnsguard.db \
    DNSGUARD_LISTS_DIR=/var/lib/dnsguard/lists \
    DNSGUARD_IP2ASN_PATH=/var/lib/dnsguard/ip2asn.tsv.gz \
    DNSGUARD_LISTEN=:8080 \
    DNSGUARD_TZSP_LISTEN=:37008

EXPOSE 8080/tcp 37008/udp
VOLUME ["/var/lib/dnsguard"]

# Dùng chính endpoint giám sát của ứng dụng: nó kiểm tra cả CSDL lẫn tuổi của truy
# vấn gần nhất, chứ không chỉ kiểm tra cổng có mở hay không.
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/health || exit 1

ENTRYPOINT ["/usr/local/bin/dnsguard"]
