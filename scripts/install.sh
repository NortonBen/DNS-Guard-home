#!/bin/sh
# Cài đặt DNSGuard trên Linux.
#
#   curl -fsSL https://raw.githubusercontent.com/benji/dnsguard/main/scripts/install.sh | sh
#
# Script tải binary tĩnh khớp kiến trúc máy, tạo người dùng hệ thống, dựng thư mục dữ
# liệu và cài dịch vụ systemd. Không cài Go, Node hay máy chủ CSDL — binary đã chứa
# sẵn giao diện, migration và động cơ SQLite.
#
# Dùng /bin/sh chứ không phải bash: một số bản Linux tối giản không có bash, và script
# cài đặt là thứ cuối cùng nên đòi hỏi thêm phụ thuộc.

set -eu

REPO="${DNSGUARD_REPO:-benji/dnsguard}"
VERSION="${DNSGUARD_VERSION:-latest}"
PREFIX="${DNSGUARD_PREFIX:-/usr/local/bin}"
DATA_DIR="${DNSGUARD_DATA_DIR:-/var/lib/dnsguard}"
CONF_DIR="${DNSGUARD_CONF_DIR:-/etc/dnsguard}"
SERVICE_USER="${DNSGUARD_USER:-dnsguard}"

say()  { printf '\033[36m→\033[0m %s\n' "$1"; }
warn() { printf '\033[33m!\033[0m %s\n' "$1" >&2; }
die()  { printf '\033[31m✗\033[0m %s\n' "$1" >&2; exit 1; }

need() {
    command -v "$1" >/dev/null 2>&1 || die "thiếu lệnh '$1' — cài nó trước rồi chạy lại"
}

# ---------------------------------------------------------------- kiểm tra môi trường

[ "$(uname -s)" = "Linux" ] || die "script này chỉ dành cho Linux. Trên macOS dùng Docker: docker compose -f deploy/docker-compose.yml up -d"

# Chạy dưới quyền root, hoặc nâng quyền bằng sudo. Không tự ý chạy sudo cho từng lệnh:
# người dùng cần thấy rõ toàn bộ script chạy với quyền gì.
if [ "$(id -u)" -ne 0 ]; then
    if command -v sudo >/dev/null 2>&1; then
        say "cần quyền root, chạy lại qua sudo"
        exec sudo -E "$0" "$@"
    fi
    die "cần chạy bằng root"
fi

need curl
need install

case "$(uname -m)" in
    x86_64|amd64)   ARCH=amd64 ;;
    aarch64|arm64)  ARCH=arm64 ;;
    *)              die "kiến trúc $(uname -m) chưa được hỗ trợ (chỉ amd64 và arm64)" ;;
esac
say "kiến trúc: $ARCH"

# ---------------------------------------------------------------- tải binary

if [ "$VERSION" = "latest" ]; then
    BASE="https://github.com/$REPO/releases/latest/download"
else
    BASE="https://github.com/$REPO/releases/download/$VERSION"
fi

TMP="$(mktemp -d)"
# Dọn file tạm kể cả khi script hỏng giữa chừng.
trap 'rm -rf "$TMP"' EXIT INT TERM

say "tải dnsguard ($VERSION, linux/$ARCH)"
curl -fsSL "$BASE/dnsguard-linux-$ARCH" -o "$TMP/dnsguard" \
    || die "không tải được binary. Kiểm tra $BASE có bản phát hành chưa."
curl -fsSL "$BASE/dnsguard-cli-linux-$ARCH" -o "$TMP/dnsguard-cli" \
    || warn "không tải được dnsguard-cli, bỏ qua tiện ích dòng lệnh"

# Kiểm tra checksum nếu bản phát hành có công bố. Không bắt buộc để việc cài không
# hỏng khi thiếu file, nhưng có thì phải khớp.
if curl -fsSL "$BASE/checksums.txt" -o "$TMP/checksums.txt" 2>/dev/null; then
    if command -v sha256sum >/dev/null 2>&1; then
        say "kiểm tra checksum"
        ( cd "$TMP" && grep -E "dnsguard(-cli)?-linux-$ARCH" checksums.txt \
            | sed "s|dnsguard-linux-$ARCH|dnsguard|; s|dnsguard-cli-linux-$ARCH|dnsguard-cli|" \
            | sha256sum -c - ) || die "checksum không khớp — dừng lại"
    fi
else
    warn "bản phát hành không có checksums.txt, bỏ qua bước kiểm tra"
fi

# ---------------------------------------------------------------- cài đặt

say "cài binary vào $PREFIX"
install -m 0755 "$TMP/dnsguard" "$PREFIX/dnsguard"
[ -f "$TMP/dnsguard-cli" ] && install -m 0755 "$TMP/dnsguard-cli" "$PREFIX/dnsguard-cli"

if ! id "$SERVICE_USER" >/dev/null 2>&1; then
    say "tạo người dùng hệ thống $SERVICE_USER"
    useradd --system --home-dir "$DATA_DIR" --create-home --shell /usr/sbin/nologin "$SERVICE_USER" \
        2>/dev/null || adduser --system --home "$DATA_DIR" --no-create-home "$SERVICE_USER"
fi

mkdir -p "$DATA_DIR/lists" "$CONF_DIR"
chown -R "$SERVICE_USER:$SERVICE_USER" "$DATA_DIR"

# Không ghi đè cấu hình đã có: chạy lại script để nâng cấp không được xóa thiết lập.
if [ ! -f "$CONF_DIR/env" ]; then
    say "tạo cấu hình mặc định $CONF_DIR/env"
    cat > "$CONF_DIR/env" <<'ENVFILE'
# Cấu hình DNSGuard. Xem toàn bộ biến ở docs/02-architecture.md §7.

DNSGUARD_LISTEN=:8080
DNSGUARD_TZSP_LISTEN=:37008

# Chỉ các dải IP này tải được /lists/*.txt. Bỏ trống là cho phép tất cả.
DNSGUARD_LISTS_ALLOW_CIDR=192.168.0.0/16,10.0.0.0/8,172.16.0.0/12

# Đặt false để không gửi truy vấn nào ra Internet ngoài phân giải DNS.
DNSGUARD_EXTERNAL_ENABLED=true

# Bỏ trống thì mật khẩu quản trị đầu tiên được sinh ngẫu nhiên và in ra log.
# DNSGUARD_ADMIN_PASSWORD=
ENVFILE
    chmod 0640 "$CONF_DIR/env"
    chown root:"$SERVICE_USER" "$CONF_DIR/env"
fi

# ---------------------------------------------------------------- dịch vụ systemd

if command -v systemctl >/dev/null 2>&1; then
    say "cài dịch vụ systemd"
    cat > /etc/systemd/system/dnsguard.service <<UNITFILE
[Unit]
Description=DNSGuard — phân tích domain và sinh blocklist DNS
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$SERVICE_USER
Group=$SERVICE_USER
EnvironmentFile=-$CONF_DIR/env
Environment=DNSGUARD_DB_PATH=$DATA_DIR/dnsguard.db
Environment=DNSGUARD_LISTS_DIR=$DATA_DIR/lists
Environment=DNSGUARD_IP2ASN_PATH=$DATA_DIR/ip2asn.tsv.gz
Environment=DNSGUARD_TRANCO_PATH=$DATA_DIR/tranco.csv.zip
ExecStart=$PREFIX/dnsguard
Restart=always
RestartSec=5

NoNewPrivileges=true
PrivateTmp=true
PrivateDevices=true
ProtectSystem=strict
ProtectHome=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
RestrictNamespaces=true
LockPersonality=true
MemoryDenyWriteExecute=true
ReadWritePaths=$DATA_DIR
MemoryMax=1G

[Install]
WantedBy=multi-user.target
UNITFILE

    systemctl daemon-reload
    systemctl enable dnsguard >/dev/null 2>&1 || true
    systemctl restart dnsguard

    sleep 2
    if systemctl is-active --quiet dnsguard; then
        say "dịch vụ đang chạy"
    else
        warn "dịch vụ chưa chạy được, xem log: journalctl -u dnsguard -n 50"
    fi
else
    warn "không có systemd — chạy tay bằng: $PREFIX/dnsguard"
fi

# ---------------------------------------------------------------- hướng dẫn tiếp theo

IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
[ -n "${IP:-}" ] || IP="<ip-máy-chủ>"

cat <<DONE

Đã cài xong DNSGuard.

  Giao diện       http://$IP:8080
  Mật khẩu admin  journalctl -u dnsguard | grep -m1 password

Còn hai bước trên thiết bị mạng:

  1) Gửi bản sao lưu lượng DNS sang đây

     /tool sniffer set filter-port=53 filter-ip-protocol=udp \\
         streaming-enabled=yes streaming-server=$IP:37008 \\
         filter-stream=yes memory-limit=100KiB
     /tool sniffer start
     /system scheduler add name=sniffer-boot start-time=startup \\
         on-event="/tool sniffer start"

     Dòng scheduler là bắt buộc: không có nó, bộ mirror dừng sau mỗi lần router khởi
     động lại và hệ thống ngừng học mà không có triệu chứng gì.

  2) Sau khi có dữ liệu, trỏ adlist về đây

     /ip dns adlist add url="http://$IP:8080/lists/ads.txt" ssl-verify=no

Vào mục Cài đặt trên giao diện để tải hai bảng tra cứu (ASN và Tranco) — thiếu Tranco
là mất lớp bảo vệ mạnh nhất chống chặn nhầm tên miền phổ biến.

DONE
