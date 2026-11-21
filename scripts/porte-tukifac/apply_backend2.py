"""
Aplica a mistiq_backend lo que tukifac hizo desde la bifurcación (30-may-2026), SIN Equipos/Shalom.
  tomar_tukifac / solo_tukifac_nuevo -> se copian de tukifac
  fusionar                            -> fusión de 3 vías (base = ancestro común)
  add_add_distintos                   -> fusión de 3 vías con la versión de tukifac más parecida a la de mistiq como base
  migraciones de tenant V133..V192 de tukifac -> se renumeran +15 (mistiq usa V133..V147 para ecommerce)
Los fines de línea se normalizan a LF para comparar y se escribe CRLF en la copia de trabajo.
"""
import json, os, re, shutil, subprocess, tempfile

SCR = r'C:\Users\win10\AppData\Local\Temp\claude\ac85129b2e8d0df2\95f9b6e9-9319-4818-a10d-2e7e346dfd1d\scratchpad'
MQ = r'C:\Users\win10\Documents\mistiq\proyecto\mistiq_backend'
TUK = os.path.join(SCR, 'tuk_head')
BASE = os.path.join(SCR, 'tuk_base')
CLONE = os.path.join(SCR, 'cmp_backend')
cls = json.load(open(os.path.join(SCR, 'cls2_backend.json')))['files']
OFFSET = 15
CR, LF = bytes([13]), bytes([10])

EXCLUDE_PREFIX = ('internal/equipos/', 'internal/shalom/', 'docs/', 'uploads/', 'storage/', '.claude/', '.github/', 'deploy/')
EXCLUDE_CONTAINS = ('equip_models', 'equip_provider_models', 'tukifac.exe')
EXCLUDE_SUFFIX = ('.db', '.exe')
KEEP_MISTIQ = {'.env.example', '.env.production.example'}
MIGR_DIR = 'pkg/database/tenantmigrations/'
MANUAL = {MIGR_DIR + 'registry.go'}


def excluded(p):
    return p.startswith(EXCLUDE_PREFIX) or any(s in p for s in EXCLUDE_CONTAINS) or p.endswith(EXCLUDE_SUFFIX) or p in KEEP_MISTIQ


def lf(b):
    return b.replace(CR + LF, LF)


def copy(rel):
    d = os.path.join(MQ, rel)
    os.makedirs(os.path.dirname(d), exist_ok=True)
    shutil.copyfile(os.path.join(TUK, rel), d)


def git(*args):
    return subprocess.run(['git', '-C', CLONE, *args], capture_output=True).stdout


def closest_base(rel, mine_lf):
    """Versión histórica de tukifac más parecida a la de mistiq (menos líneas distintas)."""
    commits = git('log', 'origin/main', '--format=%H', '--', rel).decode().split()
    best = (10 ** 9, None)
    tmp_m = os.path.join(SCR, '_m.tmp')
    open(tmp_m, 'wb').write(mine_lf)
    seen = set()
    for c in commits:
        blob = git('rev-parse', f'{c}:{rel}').decode().strip()
        if not blob or blob in seen:
            continue
        seen.add(blob)
        data = lf(git('cat-file', 'blob', blob))
        tmp_b = os.path.join(SCR, '_b.tmp')
        open(tmp_b, 'wb').write(data)
        out = subprocess.run(['git', 'diff', '--no-index', '--numstat', tmp_m, tmp_b], capture_output=True).stdout.decode().split()
        try:
            dist = int(out[0]) + int(out[1])
        except Exception:
            dist = 10 ** 8
        if dist < best[0]:
            best = (dist, data)
    return best[1], best[0]


log = {'copiados': [], 'renumerados': [], 'fusionados': [], 'conflictos': [], 'omitidos': [], 'borrados': []}

# 1) migraciones nuevas
rename_map = {}
mig_new = [p for p in cls['solo_tukifac_nuevo'] if p.startswith(MIGR_DIR)]
for p in mig_new:
    mm = re.match(r'v(\d{3})_', os.path.basename(p))
    if mm and int(mm.group(1)) >= 133:
        rename_map[int(mm.group(1))] = int(mm.group(1)) + OFFSET

for p in mig_new:
    base = os.path.basename(p)
    mm = re.match(r'v(\d{3})_(.*)', base)
    if not mm:
        log['omitidos'].append(p + ' (nombre inesperado)')
        continue
    n = int(mm.group(1))
    if n < 133:
        copy(p)
        log['copiados'].append(p)
        continue
    text = open(os.path.join(TUK, p), encoding='utf-8', newline='').read()

    def repl(m2):
        k = int(m2.group(2))
        return m2.group(1) + (f'{k + OFFSET:03d}' if k in rename_map else m2.group(2))

    text = re.sub(r'\b([Vv])(\d{3})(?=[A-Za-z_]|\b)', repl, text)
    text = re.sub(r'(Version\(\)\s*int\s*\{\s*return\s+)(\d+)',
                  lambda m3: m3.group(1) + (str(int(m3.group(2)) + OFFSET) if int(m3.group(2)) in rename_map else m3.group(2)), text)
    new_base = f'v{n + OFFSET:03d}_{mm.group(2)}'
    open(os.path.join(MQ, MIGR_DIR, new_base), 'w', encoding='utf-8', newline='').write(text)
    log['renumerados'].append(f'{base} -> {new_base}')

# 2) copiar
for cat in ('solo_tukifac_nuevo', 'tomar_tukifac'):
    for p in cls.get(cat, []):
        if p.startswith(MIGR_DIR) and cat == 'solo_tukifac_nuevo':
            continue
        if p in MANUAL or excluded(p):
            log['omitidos'].append(p)
            continue
        copy(p)
        log['copiados'].append(p)

# 3) fusiones
for cat in ('fusionar', 'add_add_distintos'):
    for p in cls.get(cat, []):
        if p in MANUAL or excluded(p):
            log['omitidos'].append(p + ' (manual / se conserva la de mistiq)')
            continue
        mine_path = os.path.join(MQ, p)
        mine = lf(open(mine_path, 'rb').read())
        was_crlf = (CR + LF) in open(mine_path, 'rb').read()
        theirs = lf(open(os.path.join(TUK, p), 'rb').read())
        base, dist = closest_base(p, mine)
        base_may = os.path.join(BASE, p)
        if os.path.exists(base_may):
            # se usa el ancestro común solo si queda más cerca que cualquier versión histórica
            b0 = lf(open(base_may, 'rb').read())
            open(os.path.join(SCR, '_b0.tmp'), 'wb').write(b0)
            open(os.path.join(SCR, '_m0.tmp'), 'wb').write(mine)
            out = subprocess.run(['git', 'diff', '--no-index', '--numstat', os.path.join(SCR, '_m0.tmp'), os.path.join(SCR, '_b0.tmp')], capture_output=True).stdout.decode().split()
            try:
                d0 = int(out[0]) + int(out[1])
            except Exception:
                d0 = 10 ** 8
            if d0 <= dist or base is None:
                base, dist = b0, d0
        tm, tb, tt = (os.path.join(SCR, n) for n in ('_mine.tmp', '_base.tmp', '_theirs.tmp'))
        open(tm, 'wb').write(mine)
        open(tb, 'wb').write(base)
        open(tt, 'wb').write(theirs)
        r = subprocess.run(['git', 'merge-file', '-L', 'MISTIQ', '-L', 'BASE', '-L', 'TUKIFAC', tm, tb, tt], capture_output=True)
        merged = open(tm, 'rb').read()
        if was_crlf:
            merged = merged.replace(LF, CR + LF)
        open(mine_path, 'wb').write(merged)
        if r.returncode == 0:
            log['fusionados'].append(p + (f' (base a distancia {dist})' if dist else ''))
        elif r.returncode > 0:
            log['conflictos'].append(f'{p}: {r.returncode} conflicto(s)' + (f' (base a distancia {dist})' if dist else ''))
        else:
            log['omitidos'].append(p + ' (error al fusionar)')

# 4) archivos que tukifac borró
for p in cls.get('solo_mistiq_borrado_en_tukifac', []):
    full = os.path.join(MQ, p)
    if p.endswith('.db'):
        continue
    if os.path.exists(full):
        os.remove(full)
        log['borrados'].append(p)

json.dump({'rename_map': rename_map, **log}, open(os.path.join(SCR, 'apply_backend2_log.json'), 'w'), indent=1)
for k, v in log.items():
    print(k, len(v))
print('FUSIONADOS LIMPIOS:'); print('\n'.join(log['fusionados']))
print('CONFLICTOS:'); print('\n'.join(log['conflictos']))
