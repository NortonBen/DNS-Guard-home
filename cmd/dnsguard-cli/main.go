// Command dnsguard-cli là tiện ích vận hành: migrate, tạo tài khoản, xuất bản thủ
// công, sao lưu.
//
// Tách khỏi binary chính để những thao tác này chạy được khi dịch vụ đang dừng, và
// để nhầm lẫn khi gõ lệnh không thể vô tình khởi động cả một máy chủ.
package main

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/benji/dnsguard/internal/auth"
	"github.com/benji/dnsguard/internal/catalog"
	"github.com/benji/dnsguard/internal/config"
	"github.com/benji/dnsguard/internal/publish"
	"github.com/benji/dnsguard/internal/store"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "migrate":
		err = cmdMigrate()
	case "create-user":
		err = cmdCreateUser(os.Args[2:])
	case "publish":
		err = cmdPublish(os.Args[2:])
	case "backup":
		err = cmdBackup(os.Args[2:])
	case "import-hosts":
		err = cmdImportHosts(os.Args[2:])
	case "version":
		fmt.Println("dnsguard-cli", version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "lệnh không rõ: %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "lỗi:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `dnsguard-cli — tiện ích vận hành DNSGuard

Cách dùng:
  dnsguard-cli migrate                        chạy migration còn thiếu
  dnsguard-cli create-user -u NAME -r ROLE    tạo tài khoản (role: admin|viewer)
  dnsguard-cli publish [-c ads,tracking]      xuất bản danh sách ngay
  dnsguard-cli backup -o FILE                 sao lưu toàn bộ CSDL
  dnsguard-cli import-hosts -f FILE -c KEY    nhập danh sách chặn từ file hosts
  dnsguard-cli version

Cấu hình đọc từ biến môi trường DNSGUARD_*, giống binary chính.
`)
}

// openStore mở CSDL theo cấu hình môi trường.
func openStore(autoMigrate bool) (*store.Store, config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, cfg, err
	}
	db, err := store.Open(cfg.DBPath, autoMigrate)
	if err != nil {
		return nil, cfg, err
	}
	return db, cfg, nil
}

func cmdMigrate() error {
	db, cfg, err := openStore(true)
	if err != nil {
		return err
	}
	defer db.Close()

	fmt.Printf("đã chạy migration trên %s\n", cfg.DBPath)
	return nil
}

func cmdCreateUser(args []string) error {
	fs := flag.NewFlagSet("create-user", flag.ExitOnError)
	username := fs.String("u", "", "tên đăng nhập")
	role := fs.String("r", store.RoleViewer, "vai trò: admin hoặc viewer")
	password := fs.String("p", "", "mật khẩu (để trống sẽ sinh ngẫu nhiên)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *username == "" {
		return fmt.Errorf("cần -u")
	}
	if *role != store.RoleAdmin && *role != store.RoleViewer {
		return fmt.Errorf("vai trò phải là admin hoặc viewer, nhận %q", *role)
	}

	db, _, err := openStore(true)
	if err != nil {
		return err
	}
	defer db.Close()

	generated := *password == ""
	if generated {
		if *password, err = randomPassword(); err != nil {
			return err
		}
	}
	hash, err := auth.HashPassword(*password)
	if err != nil {
		return err
	}
	if _, err := db.CreateUser(context.Background(), *username, hash, *role); err != nil {
		return err
	}

	fmt.Printf("đã tạo tài khoản %q với vai trò %s\n", *username, *role)
	if generated {
		fmt.Printf("mật khẩu: %s\n", *password)
		fmt.Println("lưu lại ngay — mật khẩu không hiện lại lần nữa")
	}
	return nil
}

func cmdPublish(args []string) error {
	fs := flag.NewFlagSet("publish", flag.ExitOnError)
	categories := fs.String("c", "", "danh sách phân loại ngăn bằng dấu phẩy, để trống là tất cả")
	if err := fs.Parse(args); err != nil {
		return err
	}

	db, cfg, err := openStore(true)
	if err != nil {
		return err
	}
	defer db.Close()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	publisher := publish.New(db, cfg.ListsDir, cfg.PublishMinRatio, log)

	var only []string
	if *categories != "" {
		only = strings.Split(*categories, ",")
	}

	results, err := publisher.PublishAll(context.Background(), only, "cli")
	for _, r := range results {
		state := "không đổi"
		if r.Changed {
			state = "đã ghi"
		}
		fmt.Printf("%-14s %7d mục  %s  %s\n", r.Category, r.EntryCount, state, r.Path)
	}
	return err
}

func cmdBackup(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	out := fs.String("o", "", "đường dẫn file sao lưu")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return fmt.Errorf("cần -o")
	}

	db, cfg, err := openStore(false)
	if err != nil {
		return err
	}

	// Gộp WAL vào file chính trước khi chép: nếu không, bản sao sẽ thiếu những giao
	// dịch còn nằm trong WAL.
	if err := db.Checkpoint(); err != nil {
		db.Close()
		return err
	}
	db.Close()

	if err := copyFile(cfg.DBPath, *out); err != nil {
		return err
	}

	// Thư mục lists không cần sao lưu: sinh lại được bằng `dnsguard-cli publish`.
	// Thứ thực sự không thay thế được là bảng decisions — query log tái tạo được từ
	// mạng, danh sách công khai tải lại được, nhưng lịch sử quyết định của con người
	// thì mất là mất.
	fmt.Printf("đã sao lưu %s → %s\n", cfg.DBPath, *out)
	return nil
}

func cmdImportHosts(args []string) error {
	fs := flag.NewFlagSet("import-hosts", flag.ExitOnError)
	path := fs.String("f", "", "đường dẫn file hosts")
	category := fs.String("c", "ads", "phân loại gán cho các domain nhập vào")
	reason := fs.String("reason", "nhập từ file", "lý do ghi vào nhật ký")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" {
		return fmt.Errorf("cần -f")
	}

	f, err := os.Open(*path)
	if err != nil {
		return fmt.Errorf("mở %q: %w", *path, err)
	}
	defer f.Close()

	domains, err := catalog.Parse(f, "hosts")
	if err != nil {
		return err
	}

	db, _, err := openStore(true)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx := context.Background()
	imported, skipped := 0, 0

	for _, name := range domains {
		id, err := db.EnsureDomain(ctx, name, etld1Of(name), "manual")
		if err != nil {
			skipped++
			continue
		}
		if _, err := db.ApplyDecision(ctx, store.DecisionInput{
			DomainID: id, Action: store.ActionBlock, ActorLabel: "cli",
			Reason: *reason, CategoryKey: *category, Manual: true,
		}); err != nil {
			// Domain được bảo vệ bị bỏ qua chứ không làm hỏng cả lần nhập.
			skipped++
			continue
		}
		imported++
	}

	fmt.Printf("đã nhập %d domain, bỏ qua %d\n", imported, skipped)
	return nil
}

func etld1Of(name string) string {
	parts := strings.Split(name, ".")
	if len(parts) < 2 {
		return name
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("mở %q: %w", src, err)
	}
	defer in.Close()

	if dir := filepath.Dir(dst); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("tạo thư mục %q: %w", dir, err)
		}
	}
	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("tạo %q: %w", dst, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("chép dữ liệu: %w", err)
	}
	return out.Sync()
}

func randomPassword() (string, error) {
	const alphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("sinh mật khẩu: %w", err)
	}
	for i, b := range buf {
		buf[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(buf), nil
}
