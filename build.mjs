import { spawnSync } from 'node:child_process'
import { chmodSync, copyFileSync, cpSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = dirname(fileURLToPath(import.meta.url))
const env = { ...process.env, CGO_ENABLED: '0' }

function run(label, command, args, cwd) {
  if (process.platform === 'win32' && command === 'npm') {
    // npm 的参数均为下方固定命令，不拼接用户输入。
    args = ['/d', '/s', '/c', `npm ${args.join(' ')}`]
    command = 'cmd.exe'
  }
  console.log(`▶ ${label}`)
  const started = Date.now()
  const result = spawnSync(command, args, {
    cwd,
    env,
    stdio: 'inherit',
  })
  if (result.error || result.status !== 0) {
    throw new Error(`${label}失败（${result.status ?? result.error?.message}）`)
  }
  console.log(`✓ ${label}（${((Date.now() - started) / 1000).toFixed(1)} 秒）`)
}
if (process.env.EASYBOT_SKIP_NPM_CI !== '1') {
  run('安装前端依赖', 'npm', ['ci', '--prefer-offline', '--no-audit', '--no-fund'], join(root, 'web'))
} else {
  console.log('跳过依赖安装（EASYBOT_SKIP_NPM_CI=1）')
}
run('构建前端', 'npm', ['run', 'build'], join(root, 'web'))
mkdirSync(join(root, 'bin'), { recursive: true })
const target = env.GOOS || (process.platform === 'win32' ? 'windows' : process.platform)
const output = join('bin', target === 'windows' ? 'easybotce.exe' : 'easybotce')
run('编译 Go 程序', 'go', ['build', '-trimpath', '-o', output, './cmd/easybot'], root)

const arch = env.GOARCH || ({ x64: 'amd64', ia32: '386' }[process.arch] || process.arch)
const name = `easybotce-${target}-${arch}`
const release = join(root, 'release')
if (!/^easybotce-[a-z0-9]+-[a-z0-9]+$/.test(name)) throw new Error('无效的目标平台')
mkdirSync(release, { recursive: true })
const folder = mkdtempSync(join(release, '.package-'))
try {
  copyFileSync(join(root, output), join(folder, target === 'windows' ? 'easybotce.exe' : 'easybotce'))
  copyFileSync(join(root, 'internal', 'config', 'config.example.json'), join(folder, 'config.json'))
  chmodSync(join(folder, 'config.json'), 0o600)
  cpSync(join(root, 'web', 'dist'), join(folder, 'web', 'dist'), { recursive: true })
  for (const file of ['LICENSE', 'AUTHORS.md']) copyFileSync(join(root, file), join(folder, file))
  writeFileSync(join(folder, '使用说明.txt'), [
    'EasybotCE 便携包',
    target === 'windows' ? '解压到可写目录后，双击 easybotce.exe 启动，运行期间保持窗口打开。' : '解压后运行 ./easybotce；若缺少执行权限，先执行 chmod +x easybotce。',
    '浏览器打开 http://127.0.0.1:5001，输入 config.json 中的管理密码登录；本机和外网访问均需登录。',
    '修改同目录 config.json 中 server.address 可调整管理服务监听地址，保存后重启生效。',
    'Bridge 配置位于 bridge.address（默认 0.0.0.0:26991）；Web 管理界面和后台服务由同一守护进程运行。',
    '旧版 .NET 配置需手动迁移；旧字段会被识别并拒绝加载。',
    '默认管理服务和 Bridge 监听全部网卡。外网访问前请配置防火墙，仅开放必要端口；管理服务使用 HTTP 时流量未加密，建议配置 TLS 后再通过公网使用。登录仅需输入管理密码，不需要用户名或独立 API Key。',
    '首次启动生成 12 位数字管理密码并写入 config.json，仅在控制台显示一次。',
    '双击启动失败时，窗口显示错误原因并等待回车关闭。',
    '请保留包内 web 目录。运行数据自动保存到 appdata 目录。',
    '升级前停止程序、备份并保留 config.json 和 appdata，再替换程序和 web 目录。',
    '默认使用 SQLite，无需安装开发环境或数据库服务器。',
  ].join('\n') + '\n', 'utf8')

  if (target === 'windows' && process.platform === 'win32') {
    env.EASYBOT_PACKAGE_DIR = folder
    env.EASYBOT_ARCHIVE_PATH = join(release, `${name}.zip`)
    run('压缩 Windows 便携包', 'powershell.exe', ['-NoProfile', '-NonInteractive', '-Command', 'Compress-Archive -Path (Join-Path $env:EASYBOT_PACKAGE_DIR "*") -DestinationPath $env:EASYBOT_ARCHIVE_PATH -Force'], root)
  } else {
    run('压缩便携包', 'tar', ['-czf', join(release, `${name}.tar.gz`), '-C', folder, '.'], root)
  }
} finally {
  // 只删除本次新建的临时打包目录，已解压的程序及数据不受影响。
  if (dirname(folder) !== release) throw new Error('无效的清理目录')
  rmSync(folder, { recursive: true, force: true })
}
console.log(`便携包已生成：${release}`)
