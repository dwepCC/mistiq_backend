#!/usr/bin/env node
// Resume los registros "rum_sample" del backend (telemetría de rendimiento del navegador).
//
// Uso (solo lectura):
//   ssh deploy@<vps> 'docker logs --since 24h tukifac-backend-go 2>&1 | grep rum_sample' | node scripts/rum-report.mjs
//   node scripts/rum-report.mjs registros.log
//
// Responde, con clientes reales: ¿los atascos de conexión (IPv6) ocurren también en producción? ¿qué vistas y
// qué llamadas API tardan más? ¿desde qué datacenter de Cloudflare se atiende a los usuarios?
import { readFileSync } from 'node:fs'

const input = process.argv[2] ? readFileSync(process.argv[2], 'utf8') : readFileSync(0, 'utf8')
const samples = []
for (const line of input.split('\n')) {
  const i = line.indexOf('{')
  if (i < 0 || !line.includes('rum_sample')) continue
  try {
    const j = JSON.parse(line.slice(i))
    if (j.msg === 'rum_sample') samples.push(j)
  } catch {
    /* línea ajena */
  }
}

const q = (arr, p) => {
  if (!arr.length) return 0
  const s = [...arr].sort((a, b) => a - b)
  return Math.round(s[Math.min(s.length - 1, Math.floor(s.length * p))])
}
const pct = (n, d) => (d ? ((n / d) * 100).toFixed(1) + '%' : '—')
const group = (arr, keyFn) => {
  const m = new Map()
  for (const x of arr) {
    const k = keyFn(x) ?? '?'
    if (!m.has(k)) m.set(k, [])
    m.get(k).push(x)
  }
  return m
}

console.log(`Muestras: ${samples.length}\n`)
const navs = samples.filter((s) => s.connect_ms !== undefined)

console.log('== Conexión por familia de IP (carga de página)')
console.log('familia  cargas  conexión p50/p95 (ms)   >900ms   >2s   >6s   | TTFB p50/p95   carga p50/p95')
for (const [fam, arr] of [...group(navs, (s) => s.ip_family).entries()].sort()) {
  const c = arr.map((s) => s.connect_ms)
  const t = arr.map((s) => s.ttfb_ms)
  const l = arr.map((s) => s.load_ms)
  console.log(
    `${String(fam).padEnd(8)} ${String(arr.length).padStart(6)}  ${String(q(c, 0.5)).padStart(5)}/${String(q(c, 0.95)).padEnd(8)}  ` +
      `${pct(c.filter((x) => x > 900).length, c.length).padStart(7)} ${pct(c.filter((x) => x > 2000).length, c.length).padStart(5)} ` +
      `${pct(c.filter((x) => x > 6000).length, c.length).padStart(5)}  | ${q(t, 0.5)}/${q(t, 0.95)}   ${q(l, 0.5)}/${q(l, 0.95)}`,
  )
}

console.log('\n== Por datacenter de Cloudflare (conexión y TTFB p50, ms)')
for (const [colo, arr] of [...group(navs, (s) => s.cf_colo).entries()].sort((a, b) => b[1].length - a[1].length)) {
  console.log(`${String(colo).padEnd(6)} n=${String(arr.length).padStart(5)}  conexión ${q(arr.map((s) => s.connect_ms), 0.5)}  ttfb ${q(arr.map((s) => s.ttfb_ms), 0.5)}`)
}

console.log('\n== Por país (n, conexión p95)')
for (const [cc, arr] of [...group(navs, (s) => s.country).entries()].sort((a, b) => b[1].length - a[1].length).slice(0, 8)) {
  console.log(`${String(cc).padEnd(4)} n=${String(arr.length).padStart(5)}  conexión p95 ${q(arr.map((s) => s.connect_ms), 0.95)}`)
}

console.log('\n== Protocolo')
for (const [p, arr] of group(navs, (s) => s.protocol).entries()) console.log(`${String(p).padEnd(9)} ${arr.length}`)

const views = samples.flatMap((s) => (s.views ?? []).map((v) => ({ ...v, fam: s.ip_family })))
console.log('\n== Vistas (tiempo hasta asentar los datos, ms)')
console.log('ruta                         n     p50    p95   | carga completa p50')
for (const [route, arr] of [...group(views, (v) => v.route).entries()].sort((a, b) => b[1].length - a[1].length).slice(0, 15)) {
  const first = arr.filter((v) => v.first).map((v) => v.settle_ms)
  console.log(`${String(route).padEnd(28)} ${String(arr.length).padStart(4)}  ${String(q(arr.map((v) => v.settle_ms), 0.5)).padStart(5)}  ${String(q(arr.map((v) => v.settle_ms), 0.95)).padStart(5)}   | ${first.length ? q(first, 0.5) : '—'}`)
}

const apis = samples.flatMap((s) => (s.api ?? []).map((a) => ({ ...a, fam: s.ip_family })))
console.log('\n== Llamadas API (duración total en el navegador, ms)')
console.log('ruta                                   llamadas   p50(máx de muestras)  p95     máx    KB')
const byRoute = group(apis, (a) => a.route)
const rows = [...byRoute.entries()].map(([route, arr]) => ({
  route,
  n: arr.reduce((s, a) => s + a.n, 0),
  p50: q(arr.map((a) => a.p50), 0.5),
  p95: q(arr.map((a) => a.p95), 0.95),
  max: Math.max(...arr.map((a) => a.max)),
  kb: Math.round(arr.reduce((s, a) => s + a.kb_avg, 0) / arr.length),
}))
for (const r of rows.sort((a, b) => b.p95 - a.p95).slice(0, 15)) {
  console.log(`${r.route.padEnd(38)} ${String(r.n).padStart(8)}   ${String(r.p50).padStart(8)}      ${String(r.p95).padStart(6)} ${String(r.max).padStart(6)} ${String(r.kb).padStart(5)}`)
}
