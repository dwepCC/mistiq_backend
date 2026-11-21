"""
Clasificación robusta (ignora diferencias de fin de línea) entre tukifac (origin/main) y su fork mistiq.
Uso: python classify2.py <clon> <salida.json> [mistiq_ref] [tukifac_ref]
Categorías: igual | solo_tukifac_nuevo | solo_tukifac_borrado_en_mistiq | solo_mistiq_nuevo | solo_mistiq_borrado_en_tukifac
            tomar_tukifac (mistiq quedó con una versión vieja de tukifac, o tukifac cambió y mistiq no)
            conservar_mistiq (tukifac no tocó el archivo desde la base)
            fusionar (ambos lo cambiaron)  |  add_add_distintos (ambos lo crearon y difieren)
"""
import subprocess, sys, json, hashlib, collections

repo, out = sys.argv[1], sys.argv[2]
mist_ref = sys.argv[3] if len(sys.argv) > 3 else 'mistiq/main'
tuk_ref = sys.argv[4] if len(sys.argv) > 4 else 'origin/main'


def git(*args, inp=None):
    r = subprocess.run(['git', '-C', repo, *args], capture_output=True, input=inp)
    if r.returncode not in (0, 1):
        raise RuntimeError(r.stderr.decode('utf-8', 'replace'))
    return r.stdout


def ls(ref):
    d = {}
    for line in git('ls-tree', '-r', '--full-tree', ref).decode('utf-8', 'replace').splitlines():
        meta, path = line.split('\t', 1)
        mode, typ, blob = meta.split()
        if typ == 'blob':
            d[path] = blob
    return d


_cache = {}


def nhash(blob):
    if blob is None:
        return None
    if blob in _cache:
        return _cache[blob]
    data = git('cat-file', 'blob', blob)
    h = hashlib.sha1(data.replace(b'\r\n', b'\n')).hexdigest()
    _cache[blob] = h
    return h


T, M = ls(tuk_ref), ls(mist_ref)
base = git('merge-base', tuk_ref, mist_ref).decode().strip()
B = ls(base)

# paths que difieren de verdad (normalizado)
diff = []
res = collections.defaultdict(list)
for p in sorted(set(T) | set(M)):
    t, m = T.get(p), M.get(p)
    if t and m:
        if t == m or nhash(t) == nhash(m):
            res['igual'].append(p)
        else:
            diff.append(p)
    elif t:
        res['solo_tukifac_borrado_en_mistiq' if p in B else 'solo_tukifac_nuevo'].append(p)
    else:
        res['solo_mistiq_borrado_en_tukifac' if p in B else 'solo_mistiq_nuevo'].append(p)

# historia de tukifac para los que difieren
hist = collections.defaultdict(set)
if diff:
    cmd = ['log', tuk_ref, '--raw', '--no-abbrev', '--format=', '--no-renames', '--'] + diff
    for line in git(*cmd).decode('utf-8', 'replace').splitlines():
        if not line.startswith(':'):
            continue
        meta, path = line.split('\t', 1)
        new = meta.split()[3]
        if set(new) != {'0'}:
            hist[path].add(nhash(new))

for p in diff:
    hm, ht, hb = nhash(M[p]), nhash(T[p]), nhash(B.get(p))
    if hm in hist[p] or hb == hm:
        res['tomar_tukifac'].append(p)           # mistiq nunca la tocó (o es una versión vieja de tukifac)
    elif hb is not None and hb == ht:
        res['conservar_mistiq'].append(p)        # tukifac no la tocó
    elif hb is None:
        res['add_add_distintos'].append(p)
    else:
        res['fusionar'].append(p)

summary = {k: len(v) for k, v in res.items()}
json.dump({'base': base, 'summary': summary, 'files': res}, open(out, 'w'), indent=1)
for k, v in sorted(summary.items(), key=lambda x: -x[1]):
    print(f'{v:6d} {k}')
