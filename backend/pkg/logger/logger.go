package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const dateLayout = "2006-01-02"

var (
	globalMu sync.Mutex
	daily    *dailyFile
)

// dailyFile 按日切割的日志文件写入器: 文件名 <prefix>-YYYY-MM-DD.log,
// 跨天时自动切换到当天新文件 (进程常驻运行也能正确切割)。
type dailyFile struct {
	mu      sync.Mutex
	dir     string
	prefix  string
	file    *os.File
	current string
}

func newDailyFile(dir, prefix string) (*dailyFile, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	w := &dailyFile{dir: dir, prefix: prefix}
	if err := w.rotateIfNeeded(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *dailyFile) rotateIfNeeded() error {
	today := time.Now().Format(dateLayout)
	if w.file != nil && today == w.current {
		return nil
	}
	if w.file != nil {
		_ = w.file.Close()
	}
	path := filepath.Join(w.dir, fmt.Sprintf("%s-%s.log", w.prefix, today))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		w.file = nil
		return err
	}
	w.file = f
	w.current = today
	return nil
}

func (w *dailyFile) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.rotateIfNeeded(); err != nil {
		return 0, err
	}
	return w.file.Write(p)
}

func (w *dailyFile) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		_ = w.file.Close()
		w.file = nil
	}
}

// resolveLogDir 解析日志目录:
// 1. 环境变量 LOG_DIR (显式指定时优先)
// 2. 从当前目录向上查找同时包含 backend/ 与 frontend/ 的项目根目录, 使用其 logs/
// 3. 兜底: 当前目录下 logs/
func resolveLogDir() (string, error) {
	if dir := os.Getenv("LOG_DIR"); dir != "" {
		return dir, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	cwd := dir
	for {
		if isDir(filepath.Join(dir, "backend")) && isDir(filepath.Join(dir, "frontend")) {
			return filepath.Join(dir, "logs"), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return filepath.Join(cwd, "logs"), nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// Init 初始化日志器: 标准 log 输出同时写入控制台与 <logDir>/backend-YYYY-MM-DD.log (按日切割)。
// 直接写 stdout 的组件 (如 gorm) 请改用 StdWriter() 落盘。
// release 参数保留以兼容旧签名 (时间戳现在恒启用)。
func Init(release bool) {
	log.SetFlags(log.LstdFlags)

	dir, err := resolveLogDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "logger: resolve log dir: %v (stdout only)\n", err)
		return
	}
	w, err := newDailyFile(dir, "backend")
	if err != nil {
		fmt.Fprintf(os.Stderr, "logger: open log file in %s: %v (stdout only)\n", dir, err)
		return
	}

	globalMu.Lock()
	daily = w
	globalMu.Unlock()

	log.SetOutput(io.MultiWriter(os.Stderr, w))
}

// StdWriter 返回 "原始 stdout + 日志文件" 的合并 writer,
// 供直接写 stdout 的组件 (如 gorm logger) 一并落盘; 日志文件未启用时返回原始 stdout。
func StdWriter() io.Writer {
	globalMu.Lock()
	defer globalMu.Unlock()
	if daily != nil {
		return io.MultiWriter(os.Stdout, daily)
	}
	return os.Stdout
}

// PrintfWriter 将任意 io.Writer 适配为带 Printf 方法的 writer (兼容 gorm logger.Writer 接口)
type PrintfWriter struct {
	W io.Writer
}

func (p PrintfWriter) Printf(format string, args ...interface{}) {
	_, _ = fmt.Fprintf(p.W, format, args...)
}

// Close 关闭日志文件 (优雅退出时调用)
func Close() {
	globalMu.Lock()
	defer globalMu.Unlock()
	if daily != nil {
		daily.close()
		daily = nil
	}
}
