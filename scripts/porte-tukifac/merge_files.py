"""Fusión de 3 vías de archivos concretos: python merge_files.py <repo_mistiq> <clon_cmp> <dir_tuk_head> <dir_base> archivo..."""
import os, subprocess, sys

MQ, CLONE, TUK, BASE = sys.argv[1:5]
files = sys.argv[5:]
SCR = os.path.dirname(os.path.abspath(__file__))
CR, LF = bytes([13]), bytes([10])


def lf(b):
    return b.replace(CR + LF, LF)


def git(*a):
    return subprocess.run(['git', '-C', CLONE, *a], capture_output=True).stdout


def dist(a, b):
    open(os.path.join(SCR, '_da.tmp'), 'wb').write(a)
    open(os.path.join(SCR, '_db.tmp'), 'wb').write(b)
    out = subprocess.run(['git', 'diff', '--no-index', '--numstat', os.path.join(SCR, '_da.tmp'), os.path.join(SCR, '_db.tmp')], capture_output=True).stdout.decode().split()
    try:
        return int(out[0]) + int(out[1])
    except Exception:
        return 10 ** 8


for p in files:
    mine_path = os.path.join(MQ, p)
    raw = open(mine_path, 'rb').read()
    mine, crlf = lf(raw), (CR + LF) in raw
    theirs = lf(open(os.path.join(TUK, p), 'rb').read())
    best = (10 ** 9, None)
    seen = set()
    for c in git('log', 'origin/main', '--format=%H', '--', p).decode().split():
        blob = git('rev-parse', f'{c}:{p}').decode().strip()
        if not blob or blob in seen:
            continue
        seen.add(blob)
        data = lf(git('cat-file', 'blob', blob))
        d = dist(mine, data)
        if d < best[0]:
            best = (d, data)
    bp = os.path.join(BASE, p)
    if os.path.exists(bp):
        b0 = lf(open(bp, 'rb').read())
        d0 = dist(mine, b0)
        if best[1] is None or d0 <= best[0]:
            best = (d0, b0)
    tm, tb, tt = (os.path.join(SCR, n) for n in ('_fm.tmp', '_fb.tmp', '_ft.tmp'))
    open(tm, 'wb').write(mine)
    open(tb, 'wb').write(best[1])
    open(tt, 'wb').write(theirs)
    r = subprocess.run(['git', 'merge-file', '-L', 'MISTIQ', '-L', 'BASE', '-L', 'TUKIFAC', tm, tb, tt], capture_output=True)
    merged = open(tm, 'rb').read()
    if crlf:
        merged = merged.replace(LF, CR + LF)
    open(mine_path, 'wb').write(merged)
    print(p, 'limpio' if r.returncode == 0 else f'{r.returncode} conflicto(s)', f'(base a distancia {best[0]})')
