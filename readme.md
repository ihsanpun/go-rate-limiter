### Golang rate limiter implementation

#### Endpoint yang Dilindungi (Rate Limited)

- `GET /api/test` - Test endpoint
- `GET /api/data` - Endpoint ambil data
- `POST /api/data` - Endpoint kirim data

#### Endpoint Admin (Tidak Rate Limited)

- `GET /admin/stats/{client_id}` - Ambil statistik klien
- `PUT /admin/limits/{client_id}` - Atur rate limit klien
- `POST /admin/reset/{client_id}` - Reset rate limit klien

## Instalasi

```bash
git clone https://github.com/ihsanpun/go-rate-limiter.git
cd go-ratelimiter
go mod tidy
```

## Penggunaan

### Menjalankan Server

```bash
go run main.go
```

Server akan berjalan di `localhost:8080` secara default.

### Identifikasi Klien

Layanan mendukung beberapa metode untuk identifikasi klien (berdasarkan urutan prioritas):

1. **X-API-Key header**: `X-API-Key: your-api-key`
2. **X-Client-ID header**: `X-Client-ID: your-client-id`
3. **Query parameter**: `?client_id=your-client-id`
4. **Alamat IP**: Fallback ke `RemoteAddr`

Layanan memiliki beberapa client bawaan:
1.client1 (10 request per menit)
2.client2 (20 request per menit)
3.premium (200 request per menit)

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

#### Operasi Admin

```bash
# Ambil statistik klien
curl http://localhost:8080/admin/stats/client1

# Atur rate limit kustom (50 request per menit)
curl -X PUT \
  -H "Content-Type: application/json" \
  -d '{"requests_per_window": 50, "window_duration": 60000000000}' \
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
