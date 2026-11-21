# Berkontribusi ke RAYXO

Senang kamu mau ikut membangun RAYXO. Panduan singkat ini menjelaskan cara
bertanya, melapor, dan mengirim perubahan.

## Bertanya atau mengusulkan ide

- **Pertanyaan dan ide** → [Discussions](https://github.com/fianbiasa/rayxo/discussions).
- **Laporan bug** → [Issues](https://github.com/fianbiasa/rayxo/issues) (pakai template yang tersedia).
- **Celah keamanan** → jangan dibuka di tempat publik; ikuti [Kebijakan Keamanan](SECURITY.md).

## Melaporkan bug yang baik

Sertakan:

- Versi RAYXO dan bagian yang terpengaruh (panel, agent `rayxod`, installer, atau WHMCS).
- Langkah untuk menirukannya, hasil yang diharapkan, dan hasil yang terjadi.
- OS server (Ubuntu 24.04), log yang relevan, dan screenshot kalau membantu.

## Mengirim perubahan (Pull Request)

1. **Diskusikan dulu** perubahan besar lewat Issue atau Discussion sebelum menulis kode, supaya tidak ada pekerjaan yang sia-sia.
2. Fork repo dan buat branch dari `main`.
3. Ikuti gaya kode yang sudah ada dan sertakan test untuk perubahan perilaku.
4. Pastikan semua pengecekan hijau (lihat di bawah) sebelum membuka PR.
5. Buka PR yang menjelaskan **apa** yang berubah dan **kenapa**, serta rujuk Issue terkait.

PR kecil dan fokus lebih cepat ditinjau daripada satu PR besar.

## Menjalankan proyek secara lokal

Langkah lengkap ada di [README](README.md#untuk-pengembang). Ringkasnya:

```bash
composer install && pnpm install
cp .env.example .env && php artisan key:generate
php artisan migrate --seed
pnpm run build
```

Panel dikembangkan dengan [Laravel Herd](https://herd.laravel.com/) di
`http://rayxo.test`. Agent Go ada di `agent/`.

## Pengecekan sebelum commit

Jalankan dan pastikan semuanya lolos:

| Bagian           | Perintah                                                            |
| ---------------- | ------------------------------------------------------------------- |
| Panel (PHP)      | `php artisan test`, `vendor/bin/pint`, `vendor/bin/phpstan analyse` |
| Panel (frontend) | `pnpm run check`, `pnpm run types:check`                            |
| Agent (Go)       | `cd agent && make check`                                            |

## Lisensi kontribusi

Dengan mengirim perubahan, kamu setuju karyamu dirilis di bawah lisensi proyek,
[GNU AGPL-3.0](LICENSE).

Panduan pengguna dan dokumentasi lengkap ada di
[rayxopanel.com/docs](https://rayxopanel.com/docs/). Dengan berpartisipasi, kamu
diharapkan menjaga [Kode Etik](CODE_OF_CONDUCT.md).
