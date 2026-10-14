#!/usr/bin/env node
// Generates the README/site SVG figures from web/palette.json.
//
//   node scripts/generate-svg.mjs
//
// The palette (colors and the data-palette stamp) comes from the pinned
// renderer via `node scripts/palette.js --site web/site.json --out
// web/palette.json`; this script never hardcodes colors. Every figure is
// a self-contained SVG: CSS animation only (no script), complete static
// frame, and a prefers-reduced-motion kill switch.
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const palette = JSON.parse(fs.readFileSync(path.join(root, "web/palette.json"), "utf8"));

const MONO = "'SF Mono','JetBrains Mono','Cascadia Code',ui-monospace,Menlo,Consolas,monospace";
const SANS = "-apple-system,'Segoe UI','PingFang SC','Noto Sans SC','Microsoft YaHei',sans-serif";

const css = (t) => `
    text { font-family: ${SANS}; }
    .mono { font-family: ${MONO}; }
    .drift1 { animation: drift1 ${t.driftDur}s ease-in-out infinite alternate; }
    .drift2 { animation: drift2 ${t.driftDur}s ease-in-out infinite alternate; }
    .pulse  { animation: pulse ${t.pulseDur}s ease-in-out infinite; }
    .pulse2 { animation: pulse2 ${t.pulseDur}s ease-in-out infinite; }
    .flow   { stroke-dasharray: 6 8; animation: march 1.6s linear infinite; }
    .flow-slow { stroke-dasharray: 5 9; animation: march 3.2s linear infinite; }
    .blink  { animation: blink 2.4s steps(2, jump-none) infinite; }
    @keyframes drift1 { from { transform: translate(0,0); } to { transform: translate(14px,-18px); } }
    @keyframes drift2 { from { transform: translate(0,0); } to { transform: translate(-12px,16px); } }
    @keyframes pulse  { 0%,100% { opacity: .25; } 50% { opacity: 1; } }
    @keyframes pulse2 { 0%,100% { opacity: .9; } 45% { opacity: .25; } }
    @keyframes march  { to { stroke-dashoffset: -28; } }
    @keyframes blink  { 0%,100% { opacity: 1; } 50% { opacity: .35; } }
    @media (prefers-reduced-motion: reduce) {
      .drift1,.drift2,.pulse,.pulse2,.flow,.flow-slow,.blink { animation: none; }
    }`;

function svg(name, w, h, t, body) {
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${w}" height="${h}" viewBox="0 0 ${w} ${h}" role="img" aria-label="${name}" data-palette="${palette.id}">
  <style>${css(t)}</style>
${body}
</svg>
`;
}

// Shared texture: faint particle field + grid, tinted by the palette.
function backdrop(t, w, h, seedShift = 0) {
  const dots = [];
  let x = 37 + seedShift, y = 61;
  for (let i = 0; i < 26; i++) {
    x = (x * 73 + 41) % (w - 20) + 10;
    y = (y * 131 + 29) % (h - 20) + 10;
    const r = 1 + (i % 3) * 0.7;
    dots.push(`<circle cx="${x}" cy="${y}" r="${r}" fill="${t.primary}" opacity="${0.10 + (i % 4) * 0.05}"/>`);
  }
  const lines = [];
  for (let gx = 60; gx < w; gx += 120) lines.push(`<line x1="${gx}" y1="0" x2="${gx}" y2="${h}" stroke="${t.line}" stroke-width="0.5" opacity="0.5"/>`);
  for (let gy = 60; gy < h; gy += 120) lines.push(`<line x1="0" y1="${gy}" x2="${w}" y2="${gy}" stroke="${t.line}" stroke-width="0.5" opacity="0.5"/>`);
  return `<g>${lines.join("\n")}<g class="drift1">${dots.slice(0, 13).join("")}</g><g class="drift2">${dots.slice(13).join("")}</g></g>`;
}

function glowFilter(t) {
  return `<filter id="glow" x="-40%" y="-40%" width="180%" height="180%">
    <feGaussianBlur stdDeviation="6" result="b"/>
    <feMerge><feMergeNode in="b"/><feMergeNode in="SourceGraphic"/></feMerge>
  </filter>
  <radialGradient id="halo" cx="50%" cy="42%" r="55%">
    <stop offset="0%" stop-color="${t.primary}" stop-opacity="0.16"/>
    <stop offset="60%" stop-color="${t.secondary}" stop-opacity="0.05"/>
    <stop offset="100%" stop-color="${t.bg}" stop-opacity="0"/>
  </radialGradient>`;
}

// ---------------------------------------------------------------- hero
function hero(t) {
  const W = 1200, H = 640, cx = 600, cy = 268;
  const particles = [];
  for (let i = 0; i < 14; i++) {
    const a = (i / 14) * Math.PI * 2;
    const r = 250 + (i % 4) * 22;
    const x = cx + Math.cos(a) * r, y = cy + Math.sin(a) * r * 0.82;
    particles.push(`<circle cx="${x.toFixed(1)}" cy="${y.toFixed(1)}" r="${2.2 + (i % 3)}" fill="${i % 2 ? t.primary : t.highlight}" opacity="0.5" class="${i % 2 ? "pulse" : "pulse2"}"/>`);
  }
  const orbit = [];
  for (let i = 0; i < 14; i++) {
    const a = (i / 14) * Math.PI * 2;
    orbit.push(`<line x1="${(cx + Math.cos(a) * 214).toFixed(1)}" y1="${(cy + Math.sin(a) * 176).toFixed(1)}" x2="${(cx + Math.cos(a) * 84).toFixed(1)}" y2="${(cy + Math.sin(a) * 66).toFixed(1)}" stroke="${t.line}" stroke-width="1" opacity="0.8"/>`);
  }
  return svg("ReadScope hero", W, H, t, `  <rect width="${W}" height="${H}" fill="${t.bg}"/>
  ${glowFilter(t)}
  <rect width="${W}" height="${H}" fill="url(#halo)"/>
  ${backdrop(t, W, H, 7)}
  <g>${orbit.join("")}</g>
  <g>${particles.join("")}</g>

  <!-- workspace shield: the default-deny projection -->
  <g filter="url(#glow)">
    <path d="M ${cx} ${cy - 118} L ${cx + 128} ${cy - 66} L ${cx + 128} ${cy + 52} Q ${cx + 128} ${cy + 96} ${cx} ${cy + 122} Q ${cx - 128} ${cy + 96} ${cx - 128} ${cy + 52} L ${cx - 128} ${cy - 66} Z"
      fill="${t.panel}" stroke="${t.primary}" stroke-width="2.5"/>
  </g>
  <text x="${cx}" y="${cy - 78}" text-anchor="middle" font-size="15" letter-spacing="4" fill="${t.muted}">/workspace · 默认拒绝视图</text>

  <!-- file rows: pass / deny / pass -->
  <g class="mono" font-size="17">
    <rect x="${cx - 92}" y="${cy - 54}" width="184" height="34" rx="6" fill="${t.code}" stroke="${t.secondary}" stroke-width="1"/>
    <circle cx="${cx - 74}" cy="${cy - 37}" r="4" fill="${t.primary}"/>
    <text x="${cx - 60}" y="${cy - 31}" fill="${t.ink}">src/main.go</text>
    <text x="${cx + 66}" y="${cy - 31}" text-anchor="end" font-size="13" fill="${t.primary}">pass</text>

    <rect x="${cx - 92}" y="${cy - 10}" width="184" height="34" rx="6" fill="${t.code}" stroke="${t.primary}" stroke-width="1" class="blink"/>
    <circle cx="${cx - 74}" cy="${cy + 7}" r="4" fill="${t.primary}"/>
    <text x="${cx - 60}" y="${cy + 13}" fill="${t.ink}">.env</text>
    <line x1="${cx - 62}" y1="${cy + 8}" x2="${cx - 22}" y2="${cy + 8}" stroke="${t.primary}" stroke-width="2"/>
    <text x="${cx + 66}" y="${cy + 13}" text-anchor="end" font-size="13" fill="${t.primary}">EACCES</text>

    <rect x="${cx - 92}" y="${cy + 34}" width="184" height="34" rx="6" fill="${t.code}" stroke="${t.secondary}" stroke-width="1"/>
    <circle cx="${cx - 74}" cy="${cy + 51}" r="4" fill="${t.primary}"/>
    <text x="${cx - 60}" y="${cy + 57}" fill="${t.ink}">.ssh/</text>
    <text x="${cx + 66}" y="${cy + 57}" text-anchor="end" font-size="13" fill="${t.primary}">EACCES</text>
  </g>

  <!-- inline grant card -->
  <g class="mono">
    <rect x="${cx + 172}" y="${cy + 44}" width="252" height="64" rx="10" fill="${t.panel}" stroke="${t.primary}" stroke-width="1.5"/>
    <text x="${cx + 190}" y="${cy + 70}" font-size="14" fill="${t.muted}">[readscope] y/N · TTL 30m</text>
    <text x="${cx + 190}" y="${cy + 92}" font-size="15" fill="${t.primary}">granted: .env (read-only)</text>
  </g>
  <path d="M ${cx + 130} ${cy + 70} C ${cx + 152} ${cy + 70} ${cx + 152} ${cy + 76} ${cx + 172} ${cy + 76}" stroke="${t.primary}" stroke-width="1.5" fill="none" class="flow"/>

  <!-- manifest card -->
  <g class="mono">
    <rect x="${cx - 424}" y="${cy + 44}" width="252" height="64" rx="10" fill="${t.panel}" stroke="${t.secondary}" stroke-width="1.5"/>
    <text x="${cx - 406}" y="${cy + 70}" font-size="14" fill="${t.muted}">exposure-manifest.json</text>
    <text x="${cx - 406}" y="${cy + 92}" font-size="14" fill="${t.ink}">reads · grants · env · replay ✓</text>
  </g>
  <path d="M ${cx - 172} ${cy + 70} C ${cx - 150} ${cy + 70} ${cx - 172} ${cy + 76} ${cx - 194} ${cy + 76}" stroke="${t.secondary}" stroke-width="1.5" fill="none" class="flow-slow"/>

  <!-- wordmark -->
  <text x="${cx}" y="${cy + 214}" text-anchor="middle" font-size="64" font-weight="700" letter-spacing="2" fill="${t.ink}" filter="url(#glow)">Read<tspan fill="${t.primary}">Scope</tspan></text>
  <text x="${cx}" y="${cy + 258}" text-anchor="middle" font-size="20" fill="${t.ink}">把仓库交给 Coding Agent 之前，先交给它一个默认拒绝的视图</text>
  <text x="${cx}" y="${cy + 288}" text-anchor="middle" font-size="16" fill="${t.muted}" class="mono">default-deny workspace view · JIT read grants · per-run exposure manifest</text>
`);
}

// -------------------------------------------------------- architecture
function architecture(t) {
  const W = 1200, H = 660;
  const box = (x, y, w, h, stroke, fill) =>
    `<rect x="${x}" y="${y}" width="${w}" height="${h}" rx="10" fill="${fill}" stroke="${stroke}" stroke-width="1.5"/>`;
  const wire = (d, cls = "flow", color = t.primary) =>
    `<path d="${d}" fill="none" stroke="${color}" stroke-width="1.6" class="${cls}"/>`;
  return svg("ReadScope architecture", W, H, t, `  <rect width="${W}" height="${H}" fill="${t.bg}"/>
  ${glowFilter(t)}
  ${backdrop(t, W, H, 3)}

  <text x="40" y="46" font-size="22" font-weight="600" fill="${t.ink}">架构 <tspan fill="${t.muted}" font-size="16">· 单二进制，两个角色</tspan></text>

  <!-- host zone -->
  ${box(40, 74, 300, 168, t.line, t.panel)}
  <text x="60" y="104" font-size="15" fill="${t.muted}">宿主机 · Host</text>
  ${box(60, 122, 260, 44, t.primary, t.code)}
  <text x="190" y="149" text-anchor="middle" class="mono" font-size="16" fill="${t.ink}">readscope run -- claude …</text>
  ${box(50, 182, 280, 40, t.line, t.bg)}
  <text x="190" y="207" text-anchor="middle" class="mono" font-size="13" fill="${t.muted}">docker run · SYS_ADMIN · /dev/fuse</text>

  <!-- source volume -->
  ${box(40, 470, 300, 130, t.line, t.panel)}
  <text x="60" y="500" font-size="15" fill="${t.muted}">原始工作区 · /src（bind mount）</text>
  <g class="mono" font-size="14">
    <text x="66" y="530" fill="${t.ink}">.env  ← 原始路径可读（对照组）</text>
    <text x="66" y="556" fill="${t.ink}">src/  ·  docs/  ·  .ssh/</text>
    <text x="66" y="582" fill="${t.muted}">门面不是沙箱边界，拒绝发生在视图层</text>
  </g>

  <!-- container zone -->
  ${box(400, 74, 760, 526, t.primary, t.panel)}
  <text x="424" y="104" font-size="15" fill="${t.muted}">容器内 · 同一个 readscope 二进制</text>

  ${box(424, 122, 220, 74, t.secondary, t.code)}
  <text x="534" y="152" text-anchor="middle" font-size="16" fill="${t.ink}">policy loader</text>
  <text x="534" y="176" text-anchor="middle" class="mono" font-size="13" fill="${t.muted}">readscope.yaml → 规则集</text>

  ${box(916, 122, 220, 74, t.secondary, t.code)}
  <text x="1026" y="152" text-anchor="middle" font-size="16" fill="${t.ink}">grant store <tspan fill="${t.muted}" font-size="13">授权存储</tspan></text>
  <text x="1026" y="176" text-anchor="middle" class="mono" font-size="13" fill="${t.muted}">内存 + grants.jsonl · TTL</text>

  <!-- facade core -->
  <g filter="url(#glow)">
    ${box(560, 250, 340, 150, t.primary, t.bg)}
  </g>
  <text x="730" y="290" text-anchor="middle" font-size="20" font-weight="600" fill="${t.ink}">FUSE 门面 <tspan fill="${t.primary}">/workspace</tspan></text>
  <g class="mono" font-size="14">
    <text x="730" y="322" text-anchor="middle" fill="${t.ink}">允许路径：读写透传</text>
    <text x="730" y="348" text-anchor="middle" fill="${t.primary}">其余 open/stat → EACCES</text>
    <text x="730" y="374" text-anchor="middle" fill="${t.muted}">被拒条目不进目录列表 · 授权只授 read</text>
  </g>

  ${box(424, 452, 220, 74, t.secondary, t.code)}
  <text x="534" y="482" text-anchor="middle" font-size="16" fill="${t.ink}">runner <tspan fill="${t.muted}" font-size="13">运行器</tspan></text>
  <text x="534" y="506" text-anchor="middle" class="mono" font-size="13" fill="${t.muted}">agent 进程 · cwd=视图</text>

  ${box(916, 452, 220, 74, t.secondary, t.code)}
  <text x="1026" y="482" text-anchor="middle" font-size="16" fill="${t.ink}">manifest writer <tspan fill="${t.muted}" font-size="13">清单写盘</tspan></text>
  <text x="1026" y="506" text-anchor="middle" class="mono" font-size="13" fill="${t.muted}">decisions.jsonl → manifest</text>

  <!-- wires -->
  ${wire("M 190 166 L 190 210 M 190 210 C 190 260 340 260 400 262")}
  ${wire("M 400 262 C 470 262 480 240 560 240 L 560 250", "flow", t.primary)}
  <text x="470" y="252" font-size="13" fill="${t.muted}" class="mono">挂载视图</text>

  ${wire("M 534 196 L 534 250")}
  ${wire("M 1026 196 L 1026 250", "flow", t.secondary)}
  ${wire("M 534 400 L 534 452")}
  ${wire("M 1026 452 L 1026 400", "flow-slow", t.secondary)}
  ${wire("M 644 489 L 916 489", "flow", t.primary)}
  <text x="780" y="480" text-anchor="middle" font-size="13" fill="${t.muted}" class="mono">判定实时追加</text>

  ${wire("M 730 400 L 730 452 M 730 452 C 730 440 640 430 640 452", "flow-slow", t.secondary)}
  <text x="742" y="436" font-size="13" fill="${t.muted}" class="mono">y/N · TTL</text>

  ${wire("M 340 535 L 400 535 M 400 535 C 430 535 470 520 560 430", "flow-slow", t.line)}
  <text x="352" y="522" font-size="13" fill="${t.muted}" class="mono">透传</text>

  <!-- legend -->
  <g font-size="13" fill="${t.muted}">
    <text x="40" y="640" class="mono">policy = default: deny + glob allowlist</text>
    <text x="1160" y="640" text-anchor="end" class="mono">同一二进制：宿主机编排，容器内执行</text>
  </g>
`);
}

// --------------------------------------------------------------- process
function processSvg(t) {
  const W = 1200, H = 620;
  const stage = (x, y, n, zh, en, sub, hot = false) => `
    <g>
      <rect x="${x}" y="${y}" width="204" height="96" rx="12" fill="${t.panel}" stroke="${hot ? t.primary : t.line}" stroke-width="${hot ? 1.8 : 1.4}"/>
      <circle cx="${x + 26}" cy="${y + 26}" r="13" fill="${t.code}" stroke="${hot ? t.primary : t.secondary}" stroke-width="1.4"/>
      <text x="${x + 26}" y="${y + 31}" text-anchor="middle" font-size="14" fill="${t.primary}" class="mono">${n}</text>
      <text x="${x + 48}" y="${y + 31}" font-size="16" font-weight="600" fill="${t.ink}">${zh}</text>
      <text x="${x + 18}" y="${y + 58}" font-size="13" fill="${t.muted}" class="mono">${en}</text>
      <text x="${x + 18}" y="${y + 80}" font-size="12.5" fill="${t.muted}">${sub}</text>
    </g>`;
  return svg("ReadScope process", W, H, t, `  <rect width="${W}" height="${H}" fill="${t.bg}"/>
  ${glowFilter(t)}
  ${backdrop(t, W, H, 11)}

  <text x="40" y="46" font-size="22" font-weight="600" fill="${t.ink}">一次运行 <tspan fill="${t.muted}" font-size="16">· 从默认拒绝到可审计留痕</tspan></text>

  ${stage(40, 90, 1, "加载策略", "policy.Load()", "default: deny + 规则集")}
  ${stage(276, 90, 2, "挂载视图", "fuse mount", "cwd = /workspace 视图")}
  ${stage(512, 90, 3, "运行 Agent", "exec agent", "任意命令，零配合", true)}
  ${stage(748, 90, 4, "清单落盘", "manifest", "重放 decisions.jsonl 生成")}
  ${stage(984, 90, 5, "摘要", "inspect", "一屏回答：能读到什么")}

  <path d="M 244 138 L 276 138" stroke="${t.primary}" stroke-width="1.8" class="flow" fill="none"/>
  <path d="M 480 138 L 512 138" stroke="${t.primary}" stroke-width="1.8" class="flow" fill="none"/>
  <path d="M 716 138 L 748 138" stroke="${t.primary}" stroke-width="1.8" class="flow" fill="none"/>
  <path d="M 952 138 L 984 138" stroke="${t.primary}" stroke-width="1.8" class="flow" fill="none"/>

  <!-- branch lanes from stage 3 -->
  <path d="M 614 186 L 614 250 L 200 250 L 200 300" stroke="${t.secondary}" stroke-width="1.6" class="flow-slow" fill="none"/>
  <path d="M 614 186 L 614 250 L 906 250 L 906 300" stroke="${t.primary}" stroke-width="1.6" class="flow" fill="none"/>
  <text x="560" y="222" font-size="13" fill="${t.secondary}">允许路径</text>
  <text x="762" y="222" font-size="13" fill="${t.primary}">被拒读取</text>

  <rect x="98" y="300" width="204" height="88" rx="12" fill="${t.code}" stroke="${t.secondary}" stroke-width="1.4"/>
  <text x="200" y="336" text-anchor="middle" font-size="16" fill="${t.ink}">读写透传</text>
  <text x="200" y="362" text-anchor="middle" class="mono" font-size="12" fill="${t.muted}">policy.Allow → passthrough</text>

  <rect x="804" y="300" width="204" height="88" rx="12" fill="${t.code}" stroke="${t.primary}" stroke-width="1.6"/>
  <text x="906" y="330" text-anchor="middle" font-size="16" fill="${t.ink}">终端内联审批</text>
  <text x="906" y="356" text-anchor="middle" class="mono" font-size="13" fill="${t.primary}">y/N · TTL 30m · 只读</text>
  <text x="906" y="378" text-anchor="middle" font-size="12.5" fill="${t.muted}">审批人写入授权记录</text>

  <!-- grant loop back to the view -->
  <path d="M 804 322 C 700 322 620 290 560 240" stroke="${t.primary}" stroke-width="1.6" class="flow" fill="none"/>
  <text x="596" y="308" font-size="13" fill="${t.primary}">granted → 重试成功</text>

  <rect x="1084" y="300" width="76" height="88" rx="12" fill="${t.panel}" stroke="${t.line}" stroke-width="1.4"/>
  <text x="1122" y="330" text-anchor="middle" font-size="14" fill="${t.primary}">EACCES</text>
  <text x="1122" y="352" text-anchor="middle" font-size="12.5" fill="${t.muted}">拒绝</text>

  <!-- TTL expiry note -->
  <path d="M 906 388 L 906 430" stroke="${t.line}" stroke-width="1.2" class="blink" fill="none"/>
  <rect x="804" y="430" width="204" height="52" rx="10" fill="${t.bg}" stroke="${t.line}" stroke-width="1.2"/>
  <text x="906" y="452" text-anchor="middle" font-size="13" fill="${t.muted}">TTL 到期自动收回</text>
  <text x="906" y="472" text-anchor="middle" font-size="13" fill="${t.muted}">写敏感路径永不放行</text>

  <!-- timeline footer -->
  <line x1="40" y1="546" x2="1160" y2="546" stroke="${t.line}" stroke-width="1.2"/>
  <g font-size="14" fill="${t.ink}" class="mono">
    <circle cx="200" cy="546" r="4" fill="${t.primary}" class="pulse"/>
    <circle cx="638" cy="546" r="4" fill="${t.primary}" class="pulse2"/>
    <circle cx="1000" cy="546" r="4" fill="${t.primary}" class="pulse"/>
    <text x="200" y="578" text-anchor="middle" fill="${t.muted}">decisions.jsonl 实时追加</text>
    <text x="638" y="578" text-anchor="middle" fill="${t.muted}">grants.jsonl 审批留痕</text>
    <text x="1000" y="578" text-anchor="middle" fill="${t.muted}">exposure-manifest-*.json</text>
  </g>
`);
}

// ---------------------------------------------------------- integrations
function integrations(t) {
  const W = 1200, H = 640, cx = 600, cy = 320;
  const nodes = [
    { x: cx, y: cy - 190, zh: "任意命令", en: "any command · 零配合", hot: true },
    { x: cx + 330, y: cy - 110, zh: "Docker 容器", en: "SYS_ADMIN · /dev/fuse" },
    { x: cx + 400, y: cy + 90, zh: "策略 YAML", en: "policies/default.yaml" },
    { x: cx + 120, y: cy + 210, zh: "exposure-manifest v0", en: "schema 可校验" },
    { x: cx - 260, y: cy + 180, zh: "decisions.jsonl", en: "判定日志 · 可重放" },
    { x: cx - 440, y: cy - 60, zh: "单二进制", en: "goreleaser · 6 平台" },
  ];
  const spokes = nodes.map((n, i) => {
    const midx = (cx + n.x) / 2, midy = (cy + n.y) / 2;
    const dx = n.x - cx, dy = n.y - cy, len = Math.hypot(dx, dy);
    const nx = -dy / len, ny = dx / len;
    return `<path d="M ${cx} ${cy} Q ${midx + nx * 26} ${midy + ny * 26} ${n.x} ${n.y}" fill="none" stroke="${i % 2 ? t.secondary : t.primary}" stroke-width="1.5" class="${i % 2 ? "flow-slow" : "flow"}" opacity="0.9"/>`;
  }).join("\n");
  const chips = nodes.map((n) => {
    const w = Math.max(n.zh.length * 17, n.en.length * 8.2) + 44;
    return `<g>
      <rect x="${n.x - w / 2}" y="${n.y - 34}" width="${w}" height="68" rx="12" fill="${t.panel}" stroke="${n.hot ? t.primary : t.line}" stroke-width="${n.hot ? 1.8 : 1.4}"/>
      <text x="${n.x}" y="${n.y - 6}" text-anchor="middle" font-size="17" font-weight="600" fill="${t.ink}">${n.zh}</text>
      <text x="${n.x}" y="${n.y + 18}" text-anchor="middle" font-size="12.5" fill="${t.muted}" class="mono">${n.en}</text>
    </g>`;
  }).join("\n");
  return svg("ReadScope capabilities and integration", W, H, t, `  <rect width="${W}" height="${H}" fill="${t.bg}"/>
  ${glowFilter(t)}
  ${backdrop(t, W, H, 23)}

  <text x="40" y="46" font-size="22" font-weight="600" fill="${t.ink}">能力与集成 <tspan fill="${t.muted}" font-size="16">· 一个核心原语，周围都是既有设施</tspan></text>

  ${spokes}

  <g filter="url(#glow)">
    <circle cx="${cx}" cy="${cy}" r="118" fill="${t.panel}" stroke="${t.primary}" stroke-width="2.2"/>
    <circle cx="${cx}" cy="${cy}" r="146" fill="none" stroke="${t.line}" stroke-width="1" class="pulse"/>
  </g>
  <text x="${cx}" y="${cy - 22}" text-anchor="middle" font-size="26" font-weight="700" fill="${t.ink}">Read<tspan fill="${t.primary}">Scope</tspan></text>
  <text x="${cx}" y="${cy + 8}" text-anchor="middle" font-size="14" fill="${t.muted}">核心原语</text>
  <text x="${cx}" y="${cy + 36}" text-anchor="middle" class="mono" font-size="13.5" fill="${t.primary}">least-privilege facade</text>
  <text x="${cx}" y="${cy + 58}" text-anchor="middle" class="mono" font-size="13.5" fill="${t.secondary}">+ per-run manifest</text>

  ${chips}

  <text x="40" y="${H - 18}" font-size="13" fill="${t.muted}">v0.1 范围：CLI + 门面 + 清单格式 · 托管策略与审批服务器为 v0.2 路线图（见 README）</text>
`);
}

// ----------------------------------------------------------------- main
const themes = {
  dark: {
    ...palette.dark,
    driftDur: 7,
    pulseDur: 2.6,
  },
  light: {
    ...palette.light,
    driftDur: 9,
    pulseDur: 3,
  },
};

for (const [suffix, t] of Object.entries(themes)) {
  fs.writeFileSync(path.join(root, `assets/hero-${suffix}.svg`), hero(t));
  fs.writeFileSync(path.join(root, `assets/architecture-${suffix}.svg`), architecture(t));
  fs.writeFileSync(path.join(root, `assets/process-${suffix}.svg`), processSvg(t));
  fs.writeFileSync(path.join(root, `assets/integrations-${suffix}.svg`), integrations(t));
  console.log(`assets/*-${suffix}.svg`);
}
