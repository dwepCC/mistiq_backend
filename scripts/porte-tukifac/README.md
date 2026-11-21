# Scripts del porte desde tukifac (referencia para repetirlo)

Se usaron el 21-nov-2026 para traer a Mistiq lo nuevo de `tukifac_premium` (backend y app del restaurante). Los
scripts tienen rutas fijas al entorno de esa sesión (carpeta temporal): hay que ajustarlas al repetir el proceso.
La explicación del método y del estado está en `docs/PORTE-TUKIFAC.md`.

- `classify2.py`: compara un repo de tukifac con su fork de Mistiq **ignorando fines de línea** y clasifica cada archivo
  (igual, solo tukifac, tomar tukifac, conservar mistiq, fusionar, add/add).
- `apply_backend2.py` / `apply_rest.py`: aplican esa clasificación (copiar, fusionar con `git merge-file` usando como base
  la versión histórica de tukifac más parecida a la de Mistiq, renumerar migraciones, excluir Equipos/Shalom).
- `merge_files.py`: fusión de 3 vías de archivos concretos.
- `res.py`: resuelve los conflictos que deja la fusión (`m` mistiq, `t` tukifac, `b` ambas, `@archivo` texto propio).
