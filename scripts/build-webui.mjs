// 仅复制已成功构建的前端产物；固定源/目标，拒绝符号链接，保留 embed 占位文件。
import { lstat, mkdir, readFile, readdir, rm, writeFile } from 'node:fs/promises'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = fileURLToPath(new URL('../', import.meta.url))
const source = join(root, 'frontend', 'dist')
const target = join(root, 'backend', 'internal', 'webui', 'dist')

async function requireDirectory(path) {
  const info = await lstat(path)
  if (!info.isDirectory() || info.isSymbolicLink()) {
    throw new Error(`构建目录必须是实际目录，不能是符号链接：${path}`)
  }
}

// 检查所有父目录，避免固定目标仍通过目录链接指向仓库外。
async function checkParents(parts) {
  let current = root
  await requireDirectory(current)
  for (const part of parts) {
    current = join(current, part)
    await requireDirectory(current)
  }
}

async function collect(directory, relative = '', allowPlaceholder = false) {
  const files = []
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const name = join(relative, entry.name)
    const absolute = join(directory, entry.name)
    if (entry.isSymbolicLink() || (!entry.isDirectory() && !entry.isFile())) {
      throw new Error(`构建产物不允许符号链接或特殊文件：${absolute}`)
    }
    if (entry.name.startsWith('.') && !(allowPlaceholder && name === '.gitkeep')) {
      throw new Error(`构建产物不允许隐藏文件：${absolute}`)
    }
    if (entry.isDirectory()) {
      files.push(...await collect(absolute, name, allowPlaceholder))
    } else {
      files.push({ name, absolute })
    }
  }
  return files
}

try {
  await checkParents(['frontend', 'dist'])
  await checkParents(['backend', 'internal', 'webui', 'dist'])
  const files = await collect(source)
  if (!files.some((file) => file.name === 'index.html')) {
    throw new Error('frontend/dist/index.html 缺失，请先成功执行 pnpm build')
  }
  // 删除旧产物前完成全部校验和读取；构建失败不会清除上次可用的 embed。
  const contents = await Promise.all(files.map(async (file) => ({
    name: file.name,
    data: await readFile(file.absolute),
  })))
  if (contents.find((file) => file.name === 'index.html').data.length === 0) {
    throw new Error('frontend/dist/index.html 为空，拒绝复制')
  }
  await collect(target, '', true)
  for (const name of await readdir(target)) {
    if (name !== '.gitkeep') {
      await rm(join(target, name), { recursive: true, force: false })
    }
  }
  for (const file of contents) {
    const destination = join(target, file.name)
    await mkdir(dirname(destination), { recursive: true })
    await writeFile(destination, file.data, { flag: 'wx' })
  }
  console.log(`WebUI：已复制 ${contents.length} 个文件到 backend/internal/webui/dist`)
} catch (error) {
  console.error('WebUI 产物复制失败：', error)
  process.exitCode = 1
}
