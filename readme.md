### Golang Rate Limiter Implementation

#### Endpoint yang Dilindungi (Rate Limited)

- `GET /api/test` - Endpoint test untuk validasi rate limiting
- `GET /api/data` - Endpoint pengambilan data
- `POST /api/data` - Endpoint pengiriman data

#### Endpoint Administratif (Tidak Rate Limited)

- `GET /admin/stats/{client_id}` - Ambil statistik rate limiting untuk klien
- `PUT /admin/limits/{client_id}` - Set rate limit kustom untuk klien
- `POST /admin/reset/{client_id}` - Reset counter rate limit untuk klien

## Instalasi dan Penggunaan

### Instalasi

1. Clone repository:

```bash
git clone https://github.com/ihsanpun/go-rate-limiter.git
cd go-rate-limiter
```

2. Install dependencies:

```bash
go mod tidy
```

3. Jalankan server:

```bash
go run main.go
```

Server akan berjalan di port `:8080` secara default.

### Menjalankan Test

Jalankan semua test dengan coverage:

```bash
go test -v -cover
```

Jalankan test dengan laporan coverage detail:

```bash
go test -v -coverprofile=coverage.out
go tool cover -html=coverage.out -o coverage.html
```

### Identifikasi Klien

Layanan mendukung beberapa metode identifikasi klien (berdasarkan urutan prioritas):

1. **X-API-Key header**: `X-API-Key: your-api-key`
2. **X-Client-ID header**: `X-Client-ID: your-client-id`
3. **Query parameter**: `?client_id=your-client-id`
4. **Alamat IP**: Fallback ke `RemoteAddr` jika tidak ada identifier lain

### Klien yang Sudah Dikonfigurasi

Layanan mencakup beberapa klien yang sudah dikonfigurasi untuk testing:

- **client1**: 10 request per menit
- **client2**: 50 request per menit
- **premium**: 200 request per menit

### Contoh Penggunaan

#### Panggilan API Dasar

```bash
# Test endpoint dengan client ID
curl -H "X-Client-ID: client1" http://localhost:8080/api/test

# Menggunakan API key
curl -H "X-API-Key: premium" http://localhost:8080/api/data

# Menggunakan query parameter
curl "http://localhost:8080/api/test?client_id=client2"
```

#### Operasi Administratif

```bash
# Ambil statistik klien
curl http://localhost:8080/admin/stats/client1

# Set rate limit kustom (50 request per menit)
curl -X PUT \
  -H "Content-Type: application/json" \
  -d '{"request_per_window": 50, "window_duration": 60000000000}' \
  http://localhost:8080/admin/limits/client1

# Reset rate limit klien
curl -X POST http://localhost:8080/admin/reset/client1
```

### Respons Rate Limit

Ketika rate limit terlampaui, layanan merespons dengan:

```json
{
  "error": "Rate limit exceeded",
  "client_id": "client1",
  "retry_after": 45,
  "limit": 10,
  "used": 10,
  "next_reset": "2023-11-28T10:15:00Z"
}
```

### Header Rate Limit

Semua respons sukses menyertakan informasi rate limit:

```
X-RateLimit-Limit: 100
X-RateLimit-Used: 15
X-RateLimit-Remaining: 85
X-RateLimit-Reset: 2023-11-28T10:15:00Z
```

## Keputusan Desain

### Arsitektur

1. **Desain Berbasis Interface**: Interface `RateLimiter` memungkinkan ekstensi mudah dengan algoritma yang berbeda sambil mempertahankan API yang konsisten.

2. **Implementasi Thread-Safe**: Menggunakan `sync.RWMutex` untuk manajemen klien global dan per-klien untuk memastikan keamanan thread di bawah konkurensi tinggi.

3. **Pola Middleware**: Mengimplementasikan rate limiting sebagai HTTP middleware, membuatnya dapat digunakan kembali dan mudah diintegrasikan ke aplikasi yang ada.

4. **Pemisahan Kepentingan**:

   - Logika rate limiting terpisah dari penanganan HTTP
   - Identifikasi klien dapat dikonfigurasi melalui injeksi fungsi
   - Statistik dan manajemen konfigurasi ditangani secara independen

5. **Structured Logging**: Logging komprehensif dengan informasi kontekstual untuk debugging dan monitoring.

### Struktur Data

- **Manajemen State Klien**: Setiap klien memiliki `clientState` sendiri dengan mutex individu untuk locking yang lebih halus
- **Fleksibilitas Konfigurasi**: Struct `ClientConfig` terpisah memungkinkan konfigurasi rate limit per klien
- **Pelacakan Statistik**: `ClientStats` yang detail menyediakan data monitoring yang komprehensif

### Desain HTTP

- **RESTful API**: Endpoint yang jelas dan intuitif mengikuti konvensi REST
- **Kode Status HTTP Standar**: Penggunaan yang tepat dari 429 (Too Many Requests), 404, 400, dll.
- **Header Respons**: Informasi rate limit dalam header respons untuk kesadaran klien
- **Respons JSON**: Format JSON yang konsisten untuk semua respons API

## Algoritma Rate Limiting

### Algoritma Fixed Window

**Implementasi**: `FixedWindowRateLimiter`

**Cara kerja**:

1. Setiap klien memiliki jendela waktu (misalnya 1 menit) dan batas request (misalnya 100 request)
2. Request dihitung dalam jendela saat ini
3. Ketika jendela berakhir, counter direset ke nol
4. Request diizinkan jika jumlah saat ini di bawah batas

**Keuntungan**:

- Mudah diimplementasikan dan dipahami
- Efisien memori (hanya menyimpan count dan waktu mulai jendela)
- Perilaku yang dapat diprediksi
- Performa baik di bawah beban tinggi

**Kekurangan**:

- Dapat mengizinkan lalu lintas burst pada batas jendela
- Rate limiting tidak sempurna halus
- Mungkin mengizinkan hingga 2x rate yang diinginkan dalam kasus edge

**Contoh Konfigurasi**:

```json
{
  "request_per_window": 100,
  "window_duration": 60000000000 // 1 menit dalam nanosecond
}
```

### Ekstensibilitas Algoritma Masa Depan

Desain berbasis interface memungkinkan penambahan mudah algoritma lain:

- **Sliding Window Log**: Lebih presisi tetapi intensif memori
- **Sliding Window Counter**: Keseimbangan antara fixed window dan sliding log
- **Token Bucket**: Penanganan burst yang lebih baik
- **Leaky Bucket**: Rate limiting yang halus

## Konfigurasi

### Pengaturan Default

- **Rate Limit Default**: 100 request per menit untuk klien baru
- **Port**: 8080
- **Identifikasi Klien**: Multi-method dengan fallback ke alamat IP

### Konfigurasi Dinamis

Rate limit dapat dikonfigurasi secara dinamis melalui admin API:

```bash
# Set rate limit kustom
curl -X PUT -H "Content-Type: application/json" \
  -d '{"request_per_window": 50, "window_duration": 30000000000}' \
  http://localhost:8080/admin/limits/premium-client
```

## Asumsi dan Keterbatasan

### Asumsi

1. **Penyimpanan In-Memory**: Implementasi saat ini menyimpan semua data klien dalam memori. Cocok untuk deployment single-instance.

2. **Identifikasi Klien**: Mengasumsikan klien dapat diidentifikasi dengan andal melalui header atau alamat IP.

3. **Keandalan Jaringan**: Mengasumsikan komunikasi jaringan yang dapat diandalkan untuk skenario terdistribusi.

4. **Sinkronisasi Waktu**: Jendela berbasis waktu mengasumsikan akurasi system clock.

### Keterbatasan

1. **Keterbatasan Fixed Window**:

   - Lalu lintas burst mungkin terjadi pada batas jendela
   - Distribusi rate tidak sempurna seragam
   - Mungkin mengizinkan spike rate sementara

2. **Tanpa Otentikasi**:

   - Endpoint admin tidak dilindungi
   - Identifikasi klien bergantung pada header (dapat dipalsukan)
   - Tidak ada validasi API key built-in

3. **Persistensi Konfigurasi**:
   - Konfigurasi rate limit tidak dipersisten
   - Limit kustom hilang saat restart aplikasi

### Rekomendasi Perbaikan

Untuk deployment produksi, pertimbangkan:

1. **Persistent Storage**: Backend Redis atau database untuk client state
2. **Authentication**: Manajemen dan validasi API key
3. **Monitoring**: Export metrics (Prometheus, StatsD)
4. **Advanced Algorithms**: Sliding window atau token bucket untuk rate limiting yang lebih halus

### Test Coverage

Coverage tes saat ini meliputi:

- Validasi logika rate limiting
- Fungsionalitas HTTP middleware
- Operasi endpoint administratif
- Skenario akses concurrent
- Error handling dan edge cases
- Metode identifikasi klien
- Akurasi statistik

Jalankan tes dengan:

```bash
go test -v -race -cover
```
