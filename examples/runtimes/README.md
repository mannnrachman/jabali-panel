# Runtime Examples

Contoh sederhana untuk membuktikan Jabali Panel bisa menjalankan runtime non-PHP.

## Isi folder

- `nodejs-hello/` — aplikasi Node.js
- `python-hello/` — aplikasi Python
- `go-hello/` — aplikasi Go
- `docker-hello/` — aplikasi Docker

## Cara pakai di panel

Untuk tiap domain/app:

1. Upload isi folder contoh ke `public_html` domain.
2. Di panel, ubah **Runtime Type** sesuai contoh:
   - `nodejs`
   - `python`
   - `go`
   - `docker`
3. Isi **Entry Point**:
   - Node.js: `index.js`
   - Python: `app.py`
   - Go: `main.go`
   - Docker: kosongkan untuk build dari `Dockerfile`, atau isi nama image untuk `docker pull`
4. Simpan konfigurasi runtime.
5. Tunggu reconciler / restart service.

## Catatan

- Aplikasi membaca `PORT` dari environment karena panel akan memberi port otomatis.
- Untuk Docker, container default expose port `8080`.
- Bila container memakai port lain, set env var `CONTAINER_PORT` di panel.

## Auto-deploy

Untuk mode pengembangan, auto-deploy **tidak wajib**.

Yang dimaksud auto-deploy adalah: setiap ada push ke Git repo, server otomatis update aplikasi dan restart service.

Kalau Anda masih tahap pengembangan/manual upload, cukup **CI saja** sudah cukup.
