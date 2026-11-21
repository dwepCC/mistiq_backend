"""
Resuelve conflictos de git merge-file en un archivo.
  python res.py <archivo> <elección> [<elección> ...]   (una por conflicto, en orden)
elección: m = la de mistiq | t = la de tukifac | b = ambas (mistiq y luego tukifac)
          tx = la de tukifac sin líneas que mencionen equip/shalom | bx = ambas sin equip/shalom
          @archivo.txt = texto propio
"""
import re, sys

path = sys.argv[1]
choices = sys.argv[2:]
raw = open(path, 'rb').read()
crlf = b'\r\n' in raw
text = raw.decode('utf-8').replace('\r\n', '\n')
pat = re.compile(r'<<<<<<< MISTIQ\n(.*?)=======\n(.*?)>>>>>>> TUKIFAC\n', re.S)
hunks = list(pat.finditer(text))
if len(hunks) != len(choices):
    sys.exit(f'{path}: hay {len(hunks)} conflictos y se dieron {len(choices)} elecciones')


def nox(block):
    return ''.join(l for l in block.splitlines(True) if not re.search(r'equip|shalom', l, re.I))


out, last = [], 0
for h, c in zip(hunks, choices):
    mine, theirs = h.group(1), h.group(2)
    out.append(text[last:h.start()])
    if c == 'm':
        out.append(mine)
    elif c == 't':
        out.append(theirs)
    elif c == 'b':
        out.append(mine + theirs)
    elif c == 'tx':
        out.append(nox(theirs))
    elif c == 'bx':
        out.append(mine + nox(theirs))
    elif c == 'tm':
        out.append(theirs + mine)
    elif c.startswith('bj:'):
        mid = c[3:].encode('utf-8').decode('unicode_escape')
        out.append(mine + mid + theirs)
    elif c.startswith('@'):
        out.append(open(c[1:], encoding='utf-8').read())
    else:
        sys.exit('elección inválida: ' + c)
    last = h.end()
out.append(text[last:])
res = ''.join(out)
if crlf:
    res = res.replace('\n', '\r\n')
open(path, 'wb').write(res.encode('utf-8'))
print('ok', path, len(hunks), 'conflictos resueltos')
