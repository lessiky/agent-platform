// dev 启动包装器: 运行 vite dev, 同时将输出镜像到 <项目根>/logs/frontend-YYYY-MM-DD.log (按日切割)
import { spawn } from "node:child_process";
import { closeSync, existsSync, mkdirSync, openSync, writeSync } from "node:fs";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

const frontendDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

// 解析日志目录: LOG_DIR 环境变量 > 项目根(向上查找同时含 backend/frontend 的目录)/logs > frontend/logs
function resolveLogDir() {
  if (process.env.LOG_DIR) return process.env.LOG_DIR;
  let dir = frontendDir;
  for (;;) {
    if (existsSync(path.join(dir, "backend")) && existsSync(path.join(dir, "frontend"))) {
      return path.join(dir, "logs");
    }
    const parent = path.dirname(dir);
    if (parent === dir) break;
    dir = parent;
  }
  return path.join(frontendDir, "logs");
}

const logDir = resolveLogDir();
mkdirSync(logDir, { recursive: true });

const dateOf = () => {
  const d = new Date();
  const p = (n) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
};

let fd = null;
let currentDate = null;
function writeLog(chunk) {
  const date = dateOf();
  if (fd !== null && date === currentDate) {
    writeSync(fd, chunk);
    return;
  }
  if (fd !== null) closeSync(fd);
  fd = openSync(path.join(logDir, `frontend-${date}.log`), "a");
  currentDate = date;
  writeSync(fd, chunk);
}

let loggedWriteError = false;
function tee(stream, target) {
  stream.on("data", (chunk) => {
    target.write(chunk);
    try {
      writeLog(chunk);
    } catch (err) {
      if (!loggedWriteError) {
        loggedWriteError = true;
        process.stderr.write(`[dev-logged] 日志写入失败: ${err.message}\n`);
      }
    }
  });
}

const viteJs = path.join(frontendDir, "node_modules", "vite", "bin", "vite.js");
if (!existsSync(viteJs)) {
  console.error(`[dev-logged] 未找到 ${viteJs}, 请先执行 npm install`);
  process.exit(1);
}

const child = spawn(process.execPath, [viteJs, "dev"], {
  cwd: frontendDir,
  stdio: ["inherit", "pipe", "pipe"],
  env: process.env,
});

tee(child.stdout, process.stdout);
tee(child.stderr, process.stderr);

for (const sig of ["SIGINT", "SIGTERM"]) {
  process.on(sig, () => child.kill("SIGTERM"));
}

child.on("exit", (code, signal) => {
  if (fd !== null) closeSync(fd);
  process.exit(code ?? (signal ? 1 : 0));
});