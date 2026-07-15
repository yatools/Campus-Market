package importer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "modernc.org/sqlite"

	"github.com/yatools/wutong-campus-wall/backend/internal/config"
	"github.com/yatools/wutong-campus-wall/backend/internal/database"
)

type Report struct {
	Source             string         `json:"source"`
	Target             string         `json:"target"`
	DryRun             bool           `json:"dry_run"`
	AlembicVersion     string         `json:"alembic_version"`
	Tables             map[string]int `json:"tables"`
	Rows               int            `json:"rows"`
	UploadsCopied      int            `json:"uploads_copied"`
	BackupsCopied      int            `json:"backups_copied"`
	ForeignKeyFailures int            `json:"foreign_key_failures"`
	CompletedAt        time.Time      `json:"completed_at"`
}

func Command(ctx context.Context, cfg config.Config, args []string, output io.Writer) error {
	fs := flag.NewFlagSet("import-sqlite", flag.ContinueOnError)
	fs.SetOutput(output)
	source := fs.String("sqlite", "campus.db", "Alembic 0005 SQLite 数据库")
	uploads := fs.String("uploads-source", "uploads", "原上传目录")
	backups := fs.String("backups-source", "backups", "原备份目录")
	dryRun := fs.Bool("dry-run", false, "只检查，不写入")
	reportPath := fs.String("report", "", "可选 JSON 报告路径")
	if err := fs.Parse(args); err != nil {
		return err
	}
	report, err := Import(ctx, cfg, Options{SQLitePath: *source, UploadsSource: *uploads, BackupsSource: *backups, DryRun: *dryRun})
	if err != nil {
		return err
	}
	data, _ := json.MarshalIndent(report, "", "  ")
	if _, err := output.Write(append(data, '\n')); err != nil {
		return err
	}
	if *reportPath != "" {
		if err := os.WriteFile(*reportPath, append(data, '\n'), 0o600); err != nil {
			return err
		}
	}
	return nil
}

type Options struct {
	SQLitePath, UploadsSource, BackupsSource string
	DryRun                                   bool
}

func Import(ctx context.Context, cfg config.Config, options Options) (Report, error) {
	sourcePath, err := filepath.Abs(options.SQLitePath)
	if err != nil {
		return Report{}, err
	}
	if info, err := os.Stat(sourcePath); err != nil || info.IsDir() {
		return Report{}, fmt.Errorf("SQLite 源文件不可读: %s", sourcePath)
	}
	source, err := sql.Open("sqlite", "file:"+filepath.ToSlash(sourcePath)+"?mode=ro")
	if err != nil {
		return Report{}, err
	}
	defer source.Close()
	var version string
	if err := source.QueryRowContext(ctx, "SELECT version_num FROM alembic_version LIMIT 1").Scan(&version); err != nil {
		return Report{}, fmt.Errorf("读取 Alembic 版本: %w", err)
	}
	if version != database.AlembicHead {
		return Report{}, fmt.Errorf("SQLite 版本为 %q，必须为 %s", version, database.AlembicHead)
	}
	target, err := database.Open(ctx, cfg)
	if err != nil {
		return Report{}, err
	}
	defer target.Close()
	tables, err := sourceTables(ctx, source)
	if err != nil {
		return Report{}, err
	}
	if err := validateBaselineTables(tables); err != nil {
		return Report{}, err
	}
	if options.DryRun {
		var targetTables int
		if err := target.QueryRow(ctx, "SELECT count(*) FROM pg_tables WHERE schemaname='public' AND tablename NOT IN ('goose_db_version','alembic_version')").Scan(&targetTables); err != nil {
			return Report{}, err
		}
		if targetTables > 0 {
			tables, err = commonTables(ctx, source, target)
			if err != nil {
				return Report{}, err
			}
			if err := targetMustBeEmpty(ctx, target, tables); err != nil {
				return Report{}, err
			}
		}
	} else {
		if err := database.Migrate(ctx, cfg); err != nil {
			return Report{}, err
		}
		tables, err = commonTables(ctx, source, target)
		if err != nil {
			return Report{}, err
		}
	}
	order, err := dependencyOrder(ctx, source, tables)
	if err != nil {
		return Report{}, err
	}
	report := Report{Source: sourcePath, Target: redact(cfg.DatabaseURL), DryRun: options.DryRun, AlembicVersion: version, Tables: map[string]int{}}
	for _, table := range order {
		var count int
		if err := source.QueryRowContext(ctx, "SELECT count(*) FROM "+quoteSQLite(table)).Scan(&count); err != nil {
			return Report{}, err
		}
		report.Tables[table] = count
		report.Rows += count
	}
	if options.DryRun {
		report.CompletedAt = time.Now().UTC()
		return report, nil
	}
	if err := targetMustBeEmpty(ctx, target, tables); err != nil {
		return Report{}, err
	}
	tx, err := target.Begin(ctx)
	if err != nil {
		return Report{}, err
	}
	defer tx.Rollback(ctx)
	for _, table := range order {
		if err := copyTable(ctx, source, tx, table); err != nil {
			return Report{}, fmt.Errorf("导入表 %s: %w", table, err)
		}
	}
	if err := resetSequences(ctx, tx, tables); err != nil {
		return Report{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, err
	}
	for table, want := range report.Tables {
		var got int
		if err := target.QueryRow(ctx, "SELECT count(*) FROM "+pgx.Identifier{table}.Sanitize()).Scan(&got); err != nil {
			return Report{}, err
		}
		if got != want {
			return Report{}, fmt.Errorf("表 %s 计数不一致: SQLite=%d PostgreSQL=%d", table, want, got)
		}
	}
	if failures, err := foreignKeyFailures(ctx, target); err != nil {
		return Report{}, err
	} else {
		report.ForeignKeyFailures = failures
		if failures > 0 {
			return Report{}, fmt.Errorf("导入后发现 %d 个外键异常", failures)
		}
	}
	if options.UploadsSource != "" {
		report.UploadsCopied, err = copyTree(options.UploadsSource, cfg.UploadDir)
		if err != nil {
			return Report{}, err
		}
	}
	if options.BackupsSource != "" {
		report.BackupsCopied, err = copyTree(options.BackupsSource, cfg.BackupDir)
		if err != nil {
			return Report{}, err
		}
		rows, queryErr := target.Query(ctx, "SELECT id,file_path FROM backup_jobs WHERE file_path<>''")
		if queryErr != nil {
			return Report{}, queryErr
		}
		type backupPath struct {
			id   int64
			path string
		}
		var paths []backupPath
		for rows.Next() {
			var item backupPath
			if scanErr := rows.Scan(&item.id, &item.path); scanErr != nil {
				rows.Close()
				return Report{}, scanErr
			}
			paths = append(paths, item)
		}
		rows.Close()
		for _, item := range paths {
			migrated := filepath.Join(cfg.BackupDir, filepath.Base(item.path))
			if _, statErr := os.Stat(migrated); statErr == nil {
				if _, execErr := target.Exec(ctx, "UPDATE backup_jobs SET file_path=$1 WHERE id=$2", migrated, item.id); execErr != nil {
					return Report{}, execErr
				}
			}
		}
	}
	report.CompletedAt = time.Now().UTC()
	return report, nil
}

func sourceTables(ctx context.Context, source *sql.DB) ([]string, error) {
	rows, err := source.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name <> 'alembic_version'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		result = append(result, name)
	}
	sort.Strings(result)
	return result, rows.Err()
}

func validateBaselineTables(tables []string) error {
	want := map[string]bool{}
	for _, table := range database.BaselineTables() {
		want[table] = true
	}
	for _, table := range tables {
		if !want[table] {
			return fmt.Errorf("SQLite 包含不属于 Alembic 0005 的表 %s", table)
		}
		delete(want, table)
	}
	if len(want) > 0 {
		missing := make([]string, 0, len(want))
		for table := range want {
			missing = append(missing, table)
		}
		sort.Strings(missing)
		return fmt.Errorf("SQLite 结构不完整，缺少表: %s", strings.Join(missing, ", "))
	}
	return nil
}

func commonTables(ctx context.Context, source *sql.DB, target *pgxpool.Pool) ([]string, error) {
	result, err := sourceTables(ctx, source)
	if err != nil {
		return nil, err
	}
	for _, name := range result {
		var exists bool
		if err := target.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", "public."+name).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, fmt.Errorf("PostgreSQL 基线缺少源表 %s", name)
		}
	}
	return result, nil
}

func dependencyOrder(ctx context.Context, db *sql.DB, tables []string) ([]string, error) {
	set := map[string]bool{}
	for _, t := range tables {
		set[t] = true
	}
	deps := map[string]map[string]bool{}
	reverse := map[string][]string{}
	indegree := map[string]int{}
	for _, table := range tables {
		deps[table] = map[string]bool{}
		rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_list("+quoteSQLite(table)+")")
		if err != nil {
			return nil, err
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			values := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				return nil, err
			}
			parent := asString(values[2])
			if parent != table && set[parent] {
				deps[table][parent] = true
			}
		}
		rows.Close()
		indegree[table] = len(deps[table])
		for parent := range deps[table] {
			reverse[parent] = append(reverse[parent], table)
		}
	}
	queue := []string{}
	for _, t := range tables {
		if indegree[t] == 0 {
			queue = append(queue, t)
		}
	}
	sort.Strings(queue)
	var out []string
	for len(queue) > 0 {
		t := queue[0]
		queue = queue[1:]
		out = append(out, t)
		for _, child := range reverse[t] {
			indegree[child]--
			if indegree[child] == 0 {
				queue = append(queue, child)
				sort.Strings(queue)
			}
		}
	}
	if len(out) != len(tables) {
		return nil, errors.New("SQLite 外键依赖存在无法排序的环")
	}
	return out, nil
}

func targetMustBeEmpty(ctx context.Context, pool *pgxpool.Pool, tables []string) error {
	for _, table := range tables {
		var count int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+pgx.Identifier{table}.Sanitize()).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("目标 PostgreSQL 不是空库：表 %s 有 %d 行", table, count)
		}
	}
	return nil
}

func copyTable(ctx context.Context, source *sql.DB, tx pgx.Tx, table string) error {
	rows, err := source.QueryContext(ctx, "SELECT * FROM "+quoteSQLite(table)+" ORDER BY rowid")
	if err != nil {
		return err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	types, err := targetTypes(ctx, tx, table)
	if err != nil {
		return err
	}
	quoted := make([]string, len(columns))
	params := make([]string, len(columns))
	for i, c := range columns {
		if _, ok := types[c]; !ok {
			return fmt.Errorf("目标缺少列 %s", c)
		}
		quoted[i] = pgx.Identifier{c}.Sanitize()
		params[i] = "$" + strconv.Itoa(i+1)
	}
	statement := "INSERT INTO " + pgx.Identifier{table}.Sanitize() + "(" + strings.Join(quoted, ",") + ") VALUES(" + strings.Join(params, ",") + ")"
	for rows.Next() {
		values := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		for i, c := range columns {
			values[i], err = convertValue(values[i], types[c])
			if err != nil {
				return fmt.Errorf("列 %s: %w", c, err)
			}
		}
		if _, err := tx.Exec(ctx, statement, values...); err != nil {
			return err
		}
	}
	return rows.Err()
}

func targetTypes(ctx context.Context, tx pgx.Tx, table string) (map[string]string, error) {
	rows, err := tx.Query(ctx, `SELECT column_name,data_type FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 ORDER BY ordinal_position`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var name, kind string
		if err := rows.Scan(&name, &kind); err != nil {
			return nil, err
		}
		result[name] = kind
	}
	return result, rows.Err()
}

func convertValue(value any, kind string) (any, error) {
	if value == nil {
		return nil, nil
	}
	if raw, ok := value.([]byte); ok {
		value = string(raw)
	}
	switch kind {
	case "boolean":
		switch v := value.(type) {
		case int64:
			return v != 0, nil
		case float64:
			return v != 0, nil
		case string:
			return v == "1" || strings.EqualFold(v, "true"), nil
		}
	case "timestamp with time zone", "timestamp without time zone":
		if v, ok := value.(string); ok {
			return parseTime(v)
		}
	case "date":
		if v, ok := value.(string); ok {
			return time.Parse("2006-01-02", v)
		}
	}
	return value, nil
}
func parseTime(value string) (time.Time, error) {
	layouts := []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999-07:00", "2006-01-02 15:04:05.999999", "2006-01-02 15:04:05"}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("无法解析时间 %q", value)
}

func resetSequences(ctx context.Context, tx pgx.Tx, tables []string) error {
	for _, table := range tables {
		rows, err := tx.Query(ctx, `SELECT column_name FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 AND column_default LIKE 'nextval(%'`, table)
		if err != nil {
			return err
		}
		var cols []string
		for rows.Next() {
			var c string
			if err := rows.Scan(&c); err != nil {
				rows.Close()
				return err
			}
			cols = append(cols, c)
		}
		rows.Close()
		for _, col := range cols {
			stmt := fmt.Sprintf("SELECT setval(pg_get_serial_sequence($1,$2),COALESCE(MAX(%s),1),MAX(%s) IS NOT NULL) FROM %s", pgx.Identifier{col}.Sanitize(), pgx.Identifier{col}.Sanitize(), pgx.Identifier{table}.Sanitize())
			if _, err := tx.Exec(ctx, stmt, table, col); err != nil {
				return err
			}
		}
	}
	return nil
}

func foreignKeyFailures(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	rows, err := pool.Query(ctx, `SELECT conrelid::regclass::text,conname FROM pg_constraint WHERE contype='f' AND connamespace='public'::regnamespace`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var table, name string
		if err := rows.Scan(&table, &name); err != nil {
			return 0, err
		}
		var valid bool
		if err := pool.QueryRow(ctx, "SELECT convalidated FROM pg_constraint WHERE conrelid=$1::regclass AND conname=$2", table, name).Scan(&valid); err != nil {
			return 0, err
		}
		if !valid {
			count++
		}
	}
	return count, rows.Err()
}

func copyTree(source, destination string) (int, error) {
	info, err := os.Stat(source)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !info.IsDir() {
		return 0, fmt.Errorf("源路径不是目录: %s", source)
	}
	count := 0
	err = filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		if existing, err := os.Stat(target); err == nil {
			if existing.Size() != mustSize(entry) {
				return fmt.Errorf("目标文件已存在且大小不同: %s", target)
			}
			same, err := sameSHA256(path, target)
			if err != nil {
				return err
			}
			if !same {
				return fmt.Errorf("目标文件已存在且内容不同: %s", target)
			}
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := copyFile(path, target); err != nil {
			return err
		}
		count++
		return nil
	})
	return count, err
}
func copyFile(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
func sameSHA256(a, b string) (bool, error) {
	sum := func(path string) (string, error) {
		f, err := os.Open(path)
		if err != nil {
			return "", err
		}
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return "", err
		}
		return hex.EncodeToString(h.Sum(nil)), nil
	}
	x, err := sum(a)
	if err != nil {
		return false, err
	}
	y, err := sum(b)
	return x == y, err
}
func mustSize(e os.DirEntry) int64 {
	info, _ := e.Info()
	if info == nil {
		return -1
	}
	return info.Size()
}
func quoteSQLite(value string) string { return `"` + strings.ReplaceAll(value, `"`, `""`) + `"` }
func asString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return fmt.Sprint(v)
	}
}
func redact(value string) string {
	if at := strings.LastIndex(value, "@"); at >= 0 {
		return value[at+1:]
	}
	return value
}
