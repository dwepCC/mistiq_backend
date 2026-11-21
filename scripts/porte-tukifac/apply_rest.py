"""
Aplica a mistiq_restaurant_tauri lo que Tukichef (frontend_restaurant_tenant) hizo desde la bifurcación.
  tomar_tukifac / solo_tukifac_nuevo -> se copian ; fusionar / add_add_distintos -> fusión de 3 vías (base = versión
  histórica de tukifac más parecida a la de mistiq). Se conservan el empaquetado y la marca de mistiq.
"""
import json, os, shutil, subprocess

SCR = r'C:\Users\win10\AppData\Local\Temp\claude\ac85129b2e8d0df2\95f9b6e9-9319-4818-a10d-2e7e346dfd1d\scratchpad'
MQ = r'C:\Users\win10\Documents\mistiq\proyecto\mistiq_restaurant_tauri'
TUK = os.path.join(SCR, 'rest_head')
BASE = os.path.join(SCR, 'rest_base')
CLONE = os.path.join(SCR, 'cmp_rest')
cls = json.load(open(os.path.join(SCR, 'cls2_rest.json')))['files']
CR, LF = bytes([13]), bytes([10])

# Empaquetado / marca propios de mistiq: no se tocan.
KEEP = {
    '.claude/launch.json', 'tukichef.version.json', 'src-tauri/tauri.conf.json', 'src-tauri/Cargo.toml',
    'src-tauri/Cargo.lock', 'android/app/build.gradle', 'capacitor.config.ts', 'package-lock.json',
    '.env.example', '.env.production', '.env.capacitor', '.env.development', 'tailwind.config.js',
    'src/config/branding.ts', 'src/components/layout/AppVersionBadge.tsx',
}
EXCLUDE_PREFIX = ('android/',)


def skip(p):
    return p in KEEP or p.startswith(EXCLUDE_PREFIX)


def lf(b):
    return b.replace(CR + LF, LF)


def git(*args):
    return subprocess.run(['git', '-C', CLONE, *args], capture_output=True).stdout


def closest_base(rel, mine_lf):
    commits = git('log', 'origin/main', '--format=%H', '--', rel).decode().split()
    best = (10 ** 9, None)
    tmp_m = os.path.join(SCR, '_rm.tmp')
    open(tmp_m, 'wb').write(mine_lf)
    seen = set()
    for c in commits:
        blob = git('rev-parse', f'{c}:{rel}').decode().strip()
        if not blob or blob in seen:
            continue
        seen.add(blob)
        data = lf(git('cat-file', 'blob', blob))
        tmp_b = os.path.join(SCR, '_rb.tmp')
        open(tmp_b, 'wb').write(data)
        out = subprocess.run(['git', 'diff', '--no-index', '--numstat', tmp_m, tmp_b], capture_output=True).stdout.decode().split()
        try:
            dist = int(out[0]) + int(out[1])
        except Exception:
            dist = 10 ** 8
        if dist < best[0]:
            best = (dist, data)
    return best[1], best[0]


log = {'copiados': [], 'fusionados': [], 'conflictos': [], 'omitidos': [], 'borrados': []}

for cat in ('solo_tukifac_nuevo', 'tomar_tukifac'):
    for p in cls.get(cat, []):
        if skip(p):
            log['omitidos'].append(p)
            continue
        d = os.path.join(MQ, p)
        os.makedirs(os.path.dirname(d), exist_ok=True)
        shutil.copyfile(os.path.join(TUK, p), d)
        log['copiados'].append(p)

for cat in ('fusionar', 'add_add_distintos'):
    for p in cls.get(cat, []):
        if skip(p):
            log['omitidos'].append(p + ' (se conserva la de mistiq)')
            continue
        mine_path = os.path.join(MQ, p)
        raw = open(mine_path, 'rb').read()
        mine, was_crlf = lf(raw), (CR + LF) in raw
        theirs = lf(open(os.path.join(TUK, p), 'rb').read())
        base, dist = closest_base(p, mine)
        base_may = os.path.join(BASE, p)
        if os.path.exists(base_may):
            b0 = lf(open(base_may, 'rb').read())
            open(os.path.join(SCR, '_r0.tmp'), 'wb').write(b0)
            open(os.path.join(SCR, '_rm0.tmp'), 'wb').write(mine)
            out = subprocess.run(['git', 'diff', '--no-index', '--numstat', os.path.join(SCR, '_rm0.tmp'), os.path.join(SCR, '_r0.tmp')], capture_output=True).stdout.decode().split()
            try:
                d0 = int(out[0]) + int(out[1])
            except Exception:
                d0 = 10 ** 8
            if d0 <= dist or base is None:
                base, dist = b0, d0
        tm, tb, tt = (os.path.join(SCR, n) for n in ('_rmine.tmp', '_rbase.tmp', '_rtheirs.tmp'))
        open(tm, 'wb').write(mine)
        open(tb, 'wb').write(base)
        open(tt, 'wb').write(theirs)
        r = subprocess.run(['git', 'merge-file', '-L', 'MISTIQ', '-L', 'BASE', '-L', 'TUKIFAC', tm, tb, tt], capture_output=True)
        merged = open(tm, 'rb').read()
        if was_crlf:
            merged = merged.replace(LF, CR + LF)
        open(mine_path, 'wb').write(merged)
        if r.returncode == 0:
            log['fusionados'].append(p)
        elif r.returncode > 0:
            log['conflictos'].append(f'{p}: {r.returncode} conflicto(s)')

for p in cls.get('solo_mistiq_borrado_en_tukifac', []):
    full = os.path.join(MQ, p)
    if os.path.exists(full):
        os.remove(full)
        log['borrados'].append(p)

json.dump(log, open(os.path.join(SCR, 'apply_rest_log.json'), 'w'), indent=1)
for k, v in log.items():
    print(k, len(v))
print('FUSIONADOS LIMPIOS:'); print('\n'.join(log['fusionados']))
print('CONFLICTOS:'); print('\n'.join(log['conflictos']))
