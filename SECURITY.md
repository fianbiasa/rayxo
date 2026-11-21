# Kebijakan Keamanan

RAYXO menjalankan server hosting yang dipakai banyak orang, jadi keamanan adalah
prioritas utama. Terima kasih sudah membantu menjaganya tetap aman.

## Versi yang didukung

RAYXO masih dalam tahap beta dan dirilis cepat. Perbaikan keamanan selalu
ditujukan ke **rilis terbaru**. Jalankan versi terbaru sebelum melapor, dan
perbarui panel begitu ada rilis baru (tombol **Update** di Pengaturan Server,
atau `setup.sh` rilis terbaru).

| Versi            | Didukung |
| ---------------- | -------- |
| Rilis terbaru    | ✅       |
| Versi lebih lama | ❌       |

## Melaporkan celah keamanan

**Jangan membuka Issue publik, Discussion, atau Pull Request untuk celah
keamanan.** Laporan publik memberi tahu penyerang sebelum perbaikannya tersedia.

Laporkan secara privat lewat GitHub:

1. Buka tab **[Security](https://github.com/fianbiasa/rayxo/security)** repo ini.
2. Klik **Report a vulnerability** (Private vulnerability reporting aktif).
3. Jelaskan celahnya selengkap mungkin.

Hanya maintainer yang bisa melihat laporan itu, dan kita berdiskusi di sana
sampai perbaikannya siap.

### Yang membantu kami memperbaiki lebih cepat

- Bagian mana yang terpengaruh (panel Laravel, agent `rayxod`, installer, atau modul WHMCS) dan versi RAYXO-nya.
- Langkah-langkah untuk menirukan masalahnya (proof of concept kalau ada).
- Dampaknya: apa yang bisa dilakukan penyerang, dan dalam kondisi apa.
- Saran perbaikan kalau kamu punya.

## Yang bisa kamu harapkan

- **Tanggapan awal** dalam beberapa hari kerja.
- **Konfirmasi** setelah kami berhasil menirukan masalahnya, beserta rencana perbaikan.
- **Kabar perkembangan** selama perbaikan dikerjakan.
- **Kredit** di catatan rilis saat perbaikannya terbit, kecuali kamu memilih tetap anonim.

Mohon beri kami waktu yang wajar untuk merilis perbaikan sebelum celahnya
diungkap ke publik.

## Ruang lingkup

Yang termasuk: panel Laravel, agent `rayxod`, `installer/setup.sh`, dan modul
WHMCS di repo ini. Masalah di perangkat lunak pihak ketiga yang dipasang RAYXO
(Nginx, Apache, MariaDB, PowerDNS, Postfix, Dovecot, dan lainnya) sebaiknya
dilaporkan ke proyek masing-masing, kecuali masalahnya disebabkan oleh cara
RAYXO memasang atau mengonfigurasinya.

## Prinsip desain keamanan

Beberapa hal yang sudah jadi fondasi RAYXO, berguna saat menilai sebuah laporan:

- **Panel web tidak pernah jadi root.** Panel jalan sebagai user biasa; semua perubahan sistem lewat agent `rayxod` dengan daftar aksi tertutup.
- **Setiap akun hosting terisolasi** sebagai satu user Linux dengan home `0710` dan batas resource lewat cgroups.
- **Isolasi antar akun diuji otomatis** (`agent/internal/isolation`) di server sungguhan.

Lihat [README](README.md) untuk gambaran arsitekturnya.
