#!/usr/bin/env node
/*
 * 前端静态自检 —— 在构建阶段拦住「页面卡在『正在载入控制台…』」这类问题。
 *
 * 为什么需要它：服务端有完整的 API 端到端测试，但那些测试不渲染页面。
 * index.html 用 <script src="/app.js">（普通脚本，不是 module），只要 app.js 里
 * 出现任何 module-only 语法（最典型是**顶层 await**），浏览器会直接抛 SyntaxError，
 * 整个文件不执行，页面永远停在初始的加载提示上 —— 而所有 API 测试依然全绿。
 *
 * 检查项：
 *   1. app.js 必须能按「普通脚本」语义解析（vm.Script，不允许顶层 await）
 *   2. index.html 引用的本地资源（/app.js、/style.css…）必须真实存在
 *   3. app.js 里用 $('#id') / getElementById 取的元素，必须在 index.html 中存在
 *      （拼错的 id 会让 boot() 抛 TypeError，症状同样是卡在加载提示）
 *   4. index.html 里必须没有 <script type="module">（那会改变 app.js 的解析语义）
 *
 * 用法：node scripts/check_frontend.js
 * 退出码非 0 表示有问题。
 */

'use strict';

const fs = require('fs');
const path = require('path');
const vm = require('vm');

const ROOT = path.resolve(__dirname, '..');
const WEB = path.join(ROOT, 'web');

const problems = [];
const notes = [];

function read(p) {
  return fs.readFileSync(p, 'utf8');
}

// ---------------------------------------------------------------- 1. 解析 app.js

const appPath = path.join(WEB, 'app.js');
if (!fs.existsSync(appPath)) {
  problems.push('web/app.js 不存在');
} else {
  const src = read(appPath);
  notes.push(`app.js ${Buffer.byteLength(src)} 字节`);
  try {
    // vm.Script 按 Script（经典脚本）语义解析，与 <script src> 一致。
    new vm.Script(src, { filename: 'app.js' });
  } catch (e) {
    problems.push(
      `app.js 不能按普通脚本解析：${e.name}: ${e.message}\n` +
      `      → 浏览器会直接 SyntaxError，页面将停在「正在载入控制台…」。\n` +
      `      → 若确实需要 ES module 语法，请把 index.html 改成 <script type="module">。`
    );
  }
}

// ---------------------------------------------------------------- 2. index.html

const htmlPath = path.join(WEB, 'index.html');
if (!fs.existsSync(htmlPath)) {
  problems.push('web/index.html 不存在');
}
const html = fs.existsSync(htmlPath) ? read(htmlPath) : '';

// 2a. 不允许 module 脚本（会改变 app.js 语义）
const moduleScript = html.match(/<script[^>]*type\s*=\s*["']module["'][^>]*>/i);
if (moduleScript) {
  problems.push(`index.html 使用了 ${moduleScript[0]}，但 app.js 是按经典脚本写的；`
    + '两者必须一致。');
}

// 2b. 引用的本地资源必须存在
const refRe = /(?:src|href)\s*=\s*["'](\/[^"'?#]+)["']/gi;
let m;
const refs = [];
while ((m = refRe.exec(html)) !== null) refs.push(m[1]);
for (const ref of refs) {
  const file = path.join(WEB, ref.replace(/^\//, ''));
  if (!fs.existsSync(file)) {
    problems.push(`index.html 引用了 ${ref}，但 web/${ref.replace(/^\//, '')} 不存在`);
  }
}
notes.push(`index.html 引用本地资源：${refs.join(', ') || '（无）'}`);

// ---------------------------------------------------------------- 3. id 一致性

if (html) {
  const htmlIds = new Set();
  const idRe = /\bid\s*=\s*["']([^"']+)["']/g;
  while ((m = idRe.exec(html)) !== null) htmlIds.add(m[1]);

  const appSrc = fs.existsSync(appPath) ? read(appPath) : '';
  const usedIds = new Set();
  // $('#foo') / getElementById('foo')
  const selRe = /\$\(\s*['"]#([A-Za-z][\w-]*)['"]\s*\)/g;
  while ((m = selRe.exec(appSrc)) !== null) usedIds.add(m[1]);
  const byIdRe = /getElementById\(\s*['"]([^'"]+)['"]\s*\)/g;
  while ((m = byIdRe.exec(appSrc)) !== null) usedIds.add(m[1]);

  // 动态渲染出来的节点（写入 innerHTML 的模板）不算缺失。
  const missing = [...usedIds].filter((id) => {
    if (htmlIds.has(id)) return false;
    // app.js 自己注入的模板里若有 id="<同一 id>"，视为存在
    const injected = new RegExp(`id\\s*=\\s*["']${id.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}["']`);
    return !injected.test(appSrc);
  });
  if (missing.length) {
    problems.push(`app.js 引用了 index.html 中不存在的 id：${missing.join(', ')}`);
  }
  notes.push(`id 引用检查：${usedIds.size} 个，全部可解析`);
}

// ---------------------------------------------------------------- 4. 启动兜底

if (html) {
  const fallbackIdx = html.indexOf('__wdbBooted');
  const appTagIdx = html.indexOf('<script src="/app.js"');
  if (fallbackIdx === -1) {
    problems.push('index.html 缺少 ES5 启动兜底脚本：一旦 app.js 解析失败（浏览器过旧、'
      + '语法不被支持等），页面会永远停在「正在载入控制台…」且不给任何提示。');
  } else if (appTagIdx !== -1 && fallbackIdx > appTagIdx) {
    problems.push('index.html 的启动兜底脚本必须放在 app.js 之前，否则捕获不到加载/解析失败。');
  } else {
    notes.push('启动兜底脚本存在，且位于 app.js 之前');
  }
}

// ---------------------------------------------------------------- 结果

for (const n of notes) console.log('  · ' + n);
if (problems.length) {
  console.log('');
  for (const p of problems) console.log('  [x] ' + p);
  console.log(`\n前端自检失败：${problems.length} 个问题`);
  process.exit(1);
}
console.log('\n  [+] 前端自检通过');
