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
// 将页面资源嵌入 Go 程序，避免运行目录缺失 web/dist 导致管理台不可用。
cpSync(join(root, 'web', 'dist'), join(root, 'internal', 'webui', 'dist'), { recursive: true })
mkdirSync(join(root, 'bin'), { recursive: true })
const target = env.GOOS || (process.platform === 'win32' ? 'windows' : process.platform)
const output = join('bin', target === 'windows' ? 'easybotce.exe' : 'easybotce')
run('编译 Go 程序', 'go', ['build', '-trimpath', '-o', output, './cmd/EasybotCE'], root)

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
  for (const file of ['LICENSE', 'AUTHORS.md']) copyFileSync(join(root, file), join(folder, file))
  if (target === 'windows' && process.platform === 'win32') {
    env.EASYBOT_PACKAGE_DIR = folder
    env.EASYBOT_ARCHIVE_PATH = join(release, `${name}.zip`)
    run('压缩 Windows 发布包', 'powershell.exe', ['-NoProfile', '-NonInteractive', '-Command', 'Compress-Archive -Path (Join-Path $env:EASYBOT_PACKAGE_DIR "*") -DestinationPath $env:EASYBOT_ARCHIVE_PATH -Force'], root)
  } else {
    run('压缩发布包', 'tar', ['-czf', join(release, `${name}.tar.gz`), '-C', folder, '.'], root)
  }
} finally {
  // 只删除本次新建的临时打包目录，已解压的程序及数据不受影响。
  if (dirname(folder) !== release) throw new Error('无效的清理目录')
  rmSync(folder, { recursive: true, force: true })
}
console.log(`发布包已生成：${release}`)
