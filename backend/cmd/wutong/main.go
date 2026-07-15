package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	domain "github.com/yatools/wutong-campus-wall/backend/internal/app"
	"github.com/yatools/wutong-campus-wall/backend/internal/config"
	"github.com/yatools/wutong-campus-wall/backend/internal/database"
	"github.com/yatools/wutong-campus-wall/backend/internal/httpapi"
	"github.com/yatools/wutong-campus-wall/backend/internal/importer"
	"github.com/yatools/wutong-campus-wall/backend/internal/security"
	workerpkg "github.com/yatools/wutong-campus-wall/backend/internal/worker"
)

func main() {
	if err := run(); err != nil {
		slog.Error("command_failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	command := "serve"
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command, args = args[0], args[1:]
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	configureLogging(cfg)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch command {
	case "serve":
		return serve(ctx, cfg, args)
	case "worker":
		pool, err := database.Open(ctx, cfg)
		if err != nil {
			return err
		}
		defer pool.Close()
		return workerpkg.Run(ctx, cfg, pool)
	case "migrate":
		if len(args) == 0 || args[0] == "up" {
			return database.Migrate(ctx, cfg)
		}
		if args[0] == "status" {
			return database.MigrationStatus(ctx, cfg)
		}
		return fmt.Errorf("未知 migrate 子命令 %q", args[0])
	case "create-admin":
		return createAdmin(ctx, cfg, args)
	case "verify-config":
		fmt.Printf("environment=%s\norigin=%s\ndatabase=%s\ncampus_domains=%s\n配置校验通过\n", cfg.Environment, cfg.PublicOrigin, redactDatabase(cfg.DatabaseURL), strings.Join(sortedKeys(cfg.AllowedCampusEmailDomains), ","))
		return nil
	case "import-sqlite":
		return importer.Command(ctx, cfg, args, os.Stdout)
	default:
		return fmt.Errorf("未知命令 %q", command)
	}
}

func serve(ctx context.Context, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	address := fs.String("addr", ":8000", "监听地址")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := httpapi.EnsureDirs(cfg); err != nil {
		return err
	}
	pool, err := database.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	server := &http.Server{Addr: *address, Handler: httpapi.New(cfg, pool), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 90 * time.Second, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 1 << 20}
	errorsCh := make(chan error, 1)
	go func() { slog.Info("api_started", "addr", *address); errorsCh <- server.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	case err := <-errorsCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func createAdmin(ctx context.Context, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("create-admin", flag.ContinueOnError)
	email := fs.String("email", "", "管理员邮箱")
	nickname := fs.String("nickname", "站点管理员", "昵称")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *email == "" {
		return errors.New("缺少 --email")
	}
	password := os.Getenv("INITIAL_ADMIN_PASSWORD")
	if password == "" {
		fmt.Print("管理员初始密码（至少 12 位）：")
		if term.IsTerminal(int(os.Stdin.Fd())) {
			bytes, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Println()
			if err != nil {
				return err
			}
			password = string(bytes)
		} else {
			line, err := bufio.NewReader(os.Stdin).ReadString('\n')
			if err != nil {
				return err
			}
			password = strings.TrimSpace(line)
		}
	}
	if len(password) < 12 {
		return errors.New("管理员密码至少需要 12 位")
	}
	pool, err := database.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	hash, err := security.HashPassword(password)
	if err != nil {
		return err
	}
	alias, err := randomAlias()
	if err != nil {
		return err
	}
	var id int64
	err = pool.QueryRow(ctx, `INSERT INTO users(email,password_hash,nickname,alias,campus_identity,role,status,credit,xp,dm_stranger_off,hide_online,verified_at,created_at,updated_at)
		VALUES(lower(trim($1)),$2,$3,$4,'staff','admin','active',$5,0,false,false,now(),now(),now()) RETURNING id`, *email, hash, strings.TrimSpace(*nickname), alias, domain.MaxCredit).Scan(&id)
	if err != nil {
		return err
	}
	fmt.Printf("管理员已创建：id=%d email=%s\n", id, strings.ToLower(strings.TrimSpace(*email)))
	return nil
}

func configureLogging(cfg config.Config) {
	level := slog.LevelInfo
	if strings.EqualFold(cfg.LogLevel, "DEBUG") {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))
}
func redactDatabase(v string) string {
	if i := strings.LastIndex(v, "@"); i >= 0 {
		return v[i+1:]
	}
	return v
}
func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
func randomAlias() (string, error) {
	number, err := rand.Int(rand.Reader, big.NewInt(900000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("梧桐#%06d", number.Int64()+100000), nil
}
