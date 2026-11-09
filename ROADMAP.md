# Latch — Yol Haritası

Kaynak gereksinimler: [SPEC.md](SPEC.md). Bu dosya SPEC'i sıralı, tek tek
çalıştırılabilir fazlara böler. Fazlar bağımlılık sırasındadır; atlama yok.

## Nasıl kullanılır

Her fazı ayrı bir Claude Code oturumunda şu kalıpla başlat:

```
SPEC.md, ROADMAP.md, NOTES.md ve CHANGELOG.md'yi oku.
ROADMAP.md'deki Faz N'i uygula. Faz kapsamı dışına çıkma.
Bitince: DoD maddelerini tek tek doğrula, CHANGELOG.md'ye yaz,
ROADMAP.md'de tamamlanan kutucukları işaretle, kararları NOTES.md'ye ekle.
```

Her faz şu bölümlerden oluşur:
- **Amaç** — fazın tek cümlelik hedefi.
- **Teslimatlar** — üretilecek kod/doküman (kutucuklu; faz içinde işaretlenir).
- **Testler** — bu fazda yeşil olması gereken testler.
- **DoD (bitti sayılma ölçütü)** — hepsi sağlanmadan sonraki faza geçilmez.
- **Karar noktaları** — sana sorulacak ya da NOTES.md'ye yazılacak kararlar.

Faz sonu ritüeli (her fazda aynı):
1. `make test` (Go) ve varsa `make test-ui` yeşil.
2. `CHANGELOG.md` → `[Unreleased]` altına madde.
3. Alınan kararlar `NOTES.md`'ye (tarih, karar, gerekçe, alternatifler).
4. Bu dosyada kutucuklar işaretlenir.
5. Tek commit: `phase N: <özet>`.

Büyüklük tahmini: S (yarım gün), M (1–2 gün), L (3–5 gün), XL (1 hafta+).
Tahminler tek geliştirici + Claude Code içindir.

## Fazların özeti

| # | Faz | Ana çıktı | Boyut |
|---|-----|-----------|-------|
| 0 | Tasarım dokümanları + repo iskeleti (**onay kapısı**) | docs/, PROTOCOL.md taslağı, CI | M |
| 1 | Ortak veri modeli + versiyonlu config deposu | `proto/model`, `latchd/internal/store` | M |
| 2 | Tünel protokolü + relay çekirdeği | `proto/tunnel`, `latch-relay` HTTP/TCP tünelleme | L |
| 3 | Ajan tünel istemcisi — **ilk uçtan uca** | `latchd` relay client, minimal API + CLI | L |
| 4 | Relay TLS/ACME, kurulum sihirbazı, özel alan adı | certmagic, `latch-relay install` | L |
| 5 | Relay'de politikalar + PolicySync + AccessEvent | `policy` paketi, relay'de IP/Geo/rate limit | L |
| 6 | Yerel mod: gömülü Caddy + aynı politikalar ajanda | Caddy JSON üretimi, policy middleware | L |
| 7 | Gözlemlenebilirlik | olay deposu, log, metrics, explain | M |
| 8 | Yönetim API'si (REST + WS) + kimlik doğrulama | tam API, oturum, OpenAPI | M |
| 9 | Web UI (latch-ui) | tüm ekranlar, i18n, Playwright | XL |
| 10 | Electron kabuğu | tray, daemon kontrolü, auto-update | M |
| 11 | Kurulum, paketleme, dağıtım | install komutları, Docker, release CI | M |
| 12 | Sertleştirme, kabul testleri, v1.0 | güvenlik gözden geçirme, fuzz, kabul senaryoları | M |

Fazlar arasında kritik kilometre taşları:
- **Faz 3 sonu:** `localhost:3000` relay üzerinden HTTPS'te açılıyor (dev CA ile).
- **Faz 5 sonu:** engellenen istek relay'de kesiliyor, ajan olayı görüyor.
- **Faz 6 sonu:** aynı binary hem tünel hem yerel reverse proxy.
- **Faz 9 sonu:** SPEC'teki "2 dakikada HTTPS" deneyimi arayüzden yapılabiliyor.

---

## Faz 0 — Tasarım dokümanları ve repo iskeleti (onay kapısı)

**Amaç:** SPEC'in "Çalışma biçimi §1" gereği, kod yazmadan önce yazılı tasarımı
çıkarmak ve onay almak.

**Teslimatlar**
- [x] `docs/ARCHITECTURE.md` — monorepo yapısı (`/latchd`, `/latch-relay`,
      `/latch-ui`, `/electron`, `/proto`, ek olarak `/policy` paylaşılan paket,
      `/docs`, `/deploy`), bileşen diyagramı, veri akışları (yerel / tünel),
      süreç ve port haritası.
- [x] `docs/DATA_MODEL.md` — Route, IPPolicy, RateLimitRule, HeaderRule,
      HealthCheck, Relay, AgentToken, ConfigVersion, AccessEvent; alanlar,
      doğrulama kuralları, ilişkiler, versiyonlama/geri alma semantiği.
- [x] `PROTOCOL.md` (taslak) — taşıma (TLS + yamux), control stream, mesaj
      zarfı ve kodlama, Hello/RouteAnnounce/StreamOpen/StreamClose/Ping/Pong/
      PolicySync/AccessEvent alanları, sürüm müzakeresi, hata kodları,
      istemci IP aktarımı (PROXY v2 / XFF).
- [x] `docs/API.md` — REST kaynakları ve WS kanalları (yollar, gövdeler,
      hata biçimi, sayfalama, kimlik doğrulama).
- [x] `docs/SCREENS.md` — ekran listesi, her ekranın amacı ve ana aksiyonları,
      "route ekle" iki-soru akışı, iki kurulum sihirbazı.
- [x] `docs/SECURITY.md` — SPEC'teki güvenlik gereksinimlerinin tehdit
      modeline ve tasarım kararlarına eşlenmesi.
- [x] Repo iskeleti: `go.mod`, `README.md`, `LICENSE`, `.gitignore`,
      `.editorconfig`, `Makefile` (build/test/lint/ui), `NOTES.md`,
      `CHANGELOG.md` (Keep a Changelog), `.github/workflows/ci.yml`
      (go test + golangci-lint + ui build).
- [x] `CLAUDE.md` gözden geçirilir (bu klasörde hazır var; repo kurallarıyla
      eşitlenir).

**Testler:** `go build ./...` ve CI'nin boş repo üzerinde yeşil olması.

**DoD**
- [x] Beş tasarım dokümanı yazıldı, kullanıcıya özetlendi ve **onaylandı** (2026-11-09).
- [x] Hiç özellik kodu yazılmadı (yalnızca iskelet).
- [ ] CI yeşil.

**Karar noktaları (kullanıcıya sorulur)**
- Go modül düzeni: tek `go.mod` (önerilen; `/proto` ve `/policy` doğal paylaşılır) vs. workspace.
- Modül yolu (`github.com/<kullanıcı>/latch`) ve lisans (öneri: Apache-2.0).
- UI paket yöneticisi (öneri: pnpm).
- Protokol mesaj kodlaması: uzunluk önekli JSON (basit, hata ayıklanabilir) vs. protobuf.
  Öneri: v1'de JSON; zarf sürümlü olduğu için sonradan değişebilir.

---

## Faz 1 — Ortak veri modeli + versiyonlu konfigürasyon deposu

**Amaç:** Her iki bileşenin paylaştığı tipleri ve ajanın geri alınabilir
config deposunu kurmak.

**Teslimatlar**
- [ ] `proto/model`: Route, IPPolicy, RateLimitRule, HeaderRule, HealthCheck,
      Relay, ConfigSnapshot tipleri; JSON etiketleri; `Validate()` — alan
      düzeyinde hata tipi (`FieldError{Path, Msg}`); hostname/port/CIDR
      doğrulayıcıları; ID üretimi.
- [ ] `latchd/internal/store`: SQLite (`modernc.org/sqlite`), gömülü
      migration'lar, her mutasyonda yeni `config_version` satırı (tam
      snapshot JSON + değişiklik özeti), `Rollback(version)`,
      `Diff(a, b) → etkilenen route listesi`, `Export()/Import()` (yedek/geri
      yükleme), eşzamanlı yazma kilidi.
- [ ] `latchd/internal/config`: veri dizini, dosya izinleri (0600), ayarlar
      (dinleme adresi, log seviyesi) — minimal.

**Testler:** model doğrulama tablosu testleri; store: oluştur/güncelle/sil →
versiyon artar; rollback eski durumu geri getirir; diff doğru route'ları
listeler; export→import birebir; migration ileri-geri.

**DoD**
- [ ] `go test ./proto/... ./latchd/...` yeşil, store için ≥%80 kapsama.
- [ ] `docs/DATA_MODEL.md` koda uygun (sapma varsa doküman güncellenir).

**Karar noktaları:** snapshot başına tam JSON mu (öneri; basit, küçük veri)
yoksa delta mı; saklanacak maksimum versiyon sayısı (öneri: 200, ayarlanabilir).

---

## Faz 2 — Tünel protokolü ve relay çekirdeği

**Amaç:** Relay'in ajan bağlantısını kabul edip hostname'e göre trafiği
tünele iletmesi (test içi sahte ajanla).

**Teslimatlar**
- [ ] `proto/tunnel`: mesaj zarfı (`type`, `version`, `id`, `payload`),
      codec (uzunluk önekli, maksimum boyut), tüm mesaj tipleri, sürüm
      müzakeresi, hata kodları. `PROTOCOL.md` taslaktan nihai hale getirilir.
- [ ] Oturum katmanı: yamux üzerinde control stream (ilk stream) + veri
      stream'leri; Ping/Pong ile ölü bağlantı tespiti; graceful close.
- [ ] `latch-relay/internal/auth`: token üretimi (rastgele 32 byte, base64url),
      yalnızca hash (argon2id veya SHA-256+salt; NOTES'a yaz) saklama, kapsam
      (izinli hostname/wildcard listesi), iptal, son kullanım zamanı.
- [ ] `latch-relay/internal/registry`: hostname → aktif ajan oturumu; bir
      hostname yalnızca kapsamında olan tek token'a bağlanır (çakışma/ele
      geçirme engeli); ajan düşünce rota "offline" durumuna geçer, silinmez.
- [ ] `latch-relay/internal/ingress`: 443 TLS (bu fazda dev sertifika) +
      80 → 443 yönlendirme; SNI/Host'a göre oturum bulma; HTTP isteğini yeni
      yamux stream'e iletme (hijack + raw forward; WebSocket şeffaf geçer);
      `X-Forwarded-For/-Proto/-Host` ekleme; ajan çevrimdışıysa açıklayıcı
      503 sayfası; bilinmeyen host için 404.
- [ ] TCP tünel: route başına relay'de ayrılmış port; port havuzu config'te.
- [ ] Relay SQLite deposu (token'lar, rotalar, ayarlar) + `latch-relay` CLI:
      `run`, `token create --hostnames a.relay.example.com,*.x.example.com`,
      `token list`, `token revoke`.
- [ ] Geliştirme TLS'i: `latch-relay dev-ca init` kendi CA'sını üretir;
      sertifika doğrulaması asla atlanmaz, ajan bu CA'yı `--ca-file` ile
      güvenir (SPEC güvenlik maddesi).
- [ ] İstek sınırları: başlık/gövde boyutu, `ReadHeaderTimeout`, bağlantı
      sayısı üst sınırı (temel; Faz 12'de sertleşir).

**Testler:** codec round-trip + fuzz tohumu; sahte ajanla entegrasyon: HTTPS
istek → relay → sahte ajan → yanıt; WebSocket echo; TCP tünel echo; yanlış
token reddi; kapsam dışı hostname reddi; iki ajan aynı hostname → ikinci red;
ajan kopunca 503.

**DoD**
- [ ] `PROTOCOL.md` koda birebir uygun; her mesaj tipi için örnek.
- [ ] Entegrasyon testleri yeşil (`go test ./latch-relay/...`).
- [ ] `latch-relay run --dev` ile elle test: `curl --cacert` ile 503 sayfası.

**Karar noktaları:** HTTP iletimi "raw hijack" (öneri; WebSocket/HTTP2 bedava,
relay istek gövdesine dokunmaz) vs. `httputil.ReverseProxy`; TCP port havuzu
aralığı; token hash algoritması.

---

## Faz 3 — Ajan tünel istemcisi: ilk uçtan uca

**Amaç:** `latchd` yerel bir portu relay üzerinden HTTPS'te açar ve bağlantı
kopunca kendiliğinden toparlanır.

**Teslimatlar**
- [ ] `latchd/internal/tunnel`: relay istemcisi — TLS bağlan (sistem kökleri
      veya `--ca-file`), Hello, store'daki tunnel route'ları için
      RouteAnnounce, stream open → yerel hedefe dial (`net.Dial` + iki yönlü
      kopya), Ping/Pong, graceful close.
- [ ] Yeniden bağlanma: üstel geri çekilme + jitter (1s → 60s üst sınır),
      bağlanınca rotaları yeniden duyurma, durum makinesi
      (`connecting/connected/backoff/stopped`) ve sayaçlar.
- [ ] Birden fazla relay desteği (store'daki her Relay için bir istemci).
- [ ] Route değişikliğinde (store olayı) anında RouteAnnounce farkı gönderme.
- [ ] Sürüm uyumsuzluğunda net hata mesajı (Hello müzakeresi).
- [ ] Minimal yönetim API'si (`127.0.0.1`, kimlik doğrulamasız bu fazda):
      `GET/POST/PUT/DELETE /api/routes`, `/api/relays`, `GET /api/status`.
- [ ] `latchd` CLI (API'yi çağırır): `run`, `relay add --url --token
      [--ca-file]`, `route add --target 127.0.0.1:3000 --tunnel <relay>
      --hostname app.relay.example.com`, `route list`, `status`.

**Testler:** uçtan uca (tek process içinde relay + latchd + yerel http server):
HTTPS istek yerel sunucuya ulaşır, istemci IP XFF'te doğru; WebSocket; relay
bağlantısı kesilince ajan geri çekilmeyle bağlanır ve rota tekrar çalışır
(test 10 s altında); relay yeniden başlatılınca aynı; yerel hedef kapalıyken
relay 502.

**DoD**
- [ ] `/etc/hosts` + dev CA ile elle demo: `localhost:3000` →
      `https://app.relay.local`. Demo adımları `docs/DEV.md`'de.
- [ ] Ajanı öldürüp açınca kullanıcı müdahalesi olmadan tünel geri geliyor.

**Karar noktaları:** backoff parametreleri; birden fazla relay'e aynı
hostname'in duyurulması yasak (öneri) mı?

---

## Faz 4 — Relay TLS/ACME, kurulum sihirbazı, özel alan adı

**Amaç:** Relay'in gerçek bir VPS'te tek komutla kurulup geçerli
sertifikalarla çalışması.

**Teslimatlar**
- [ ] certmagic entegrasyonu: wildcard DNS-01 (libdns: Cloudflare, Route53,
      Hetzner, DigitalOcean) ve "manuel TXT" modu (sihirbaz TXT kaydını
      gösterip bekler); alternatif mod: alt alan başına TLS-ALPN-01 on-demand.
- [ ] Özel alan adı: on-demand ACME, "izin" fonksiyonu yalnızca kayıtlı ve
      CNAME/A kaydı relay'e dönen hostname'lere sertifika alır (ACME kota
      ve kötüye kullanım koruması); doğrulama sonucu ajana bildirilir.
- [ ] Ajan tarafı: 7443 için sertifika doğrulaması sistem kökleriyle; dev CA
      yalnızca açıkça verilirse.
- [ ] `latch-relay install`: etkileşimli sihirbaz (alan adı, e-posta, DNS
      sağlayıcı + API anahtarı veya manuel), wildcard DNS'in bu sunucunun
      IP'sine çözüldüğünü kontrol, config yazma (`/etc/latch-relay/`), systemd
      unit, ilk token üretimi ve "ajana yapıştırılacak" bağlantı dizesi
      (`latch://host:7443?token=...` biçimi PROTOCOL.md'de).
- [ ] `latch-relay status`, `latch-relay doctor` (port, DNS, sertifika,
      saat kontrolü).
- [ ] `deploy/install.sh`: tek satır kurulum betiği (binary indir, `install`
      çalıştır); Faz 9'daki "Relay'ini kur" sihirbazı bunu üretir.

**Testler:** izin fonksiyonu birim testleri (kayıtlı/kayıtsız/CNAME yanlış);
sihirbaz DNS kontrolü sahte resolver ile; `make test-acme` hedefi Pebble
ile opsiyonel entegrasyon (CI'da ayrı iş).

**DoD**
- [ ] Gerçek bir VPS'te `curl -sL .../install.sh | sh` → sihirbaz → geçerli
      wildcard sertifika; `https://x.relay.<alan>` ajan yokken 503 sayfası.
- [ ] Özel alan adı CNAME ile çalışıyor.

**Karar noktaları:** Let's Encrypt staging anahtarı sihirbazda seçenek;
desteklenecek ilk DNS sağlayıcı listesi doğrulanır.

---

## Faz 5 — Relay'de politikalar: IP, GeoIP, rate limit, header + PolicySync + AccessEvent

**Amaç:** Engellenen trafiğin ajana hiç inmemesi ve kullanıcının olayları
ajanda görmesi.

**Teslimatlar**
- [ ] `/policy` paylaşılan paket (Faz 6'da Caddy middleware olarak da
      kullanılır): güvenilir proxy listesine göre istemci IP çözümü; IP/CIDR
      allow-deny değerlendirme (sıra ve varsayılan davranış); GeoIP (mmdb
      okuyucu, ülke kodu); sliding-window rate limiter (anahtar: IP, header,
      path, birleşim; bellek içi, LRU sınırlı); reddetme davranışı
      (403/404/bağlantı kes; 429 + Retry-After veya gecikme); header
      ekle/sil kuralları; `Explain(req) → Decision{Rule, Where, Result}`.
- [ ] `proto/tunnel`: `PolicySync` (tam snapshot + monoton versiyon + ack),
      `AccessEvent` (zaman, hostname, IP, ülke, metod, path, kod, süre,
      engellenme nedeni, kaynak=relay).
- [ ] Relay: politika motorunu ingress'e bağlama; politikaları SQLite'ta
      token/ajan bazında kalıcı tutma (ajan çevrimdışıyken de uygulanır);
      ack ile versiyon bildirimi.
- [ ] Ajan: route/policy değişince PolicySync gönderme; ack versiyonunu
      izleme; eşleşmezse `relay.syncState = out_of_sync` (Faz 9'da uyarı);
      gelen AccessEvent'leri halka tampona alma (Faz 7'de kalıcı olacak).
- [ ] GeoIP veritabanı dağıtımı: relay config'te dosya yolu; ajan
      `PolicySync` içinde "geoip gerekli" bildirir, dosya yoksa relay ülke
      kurallarını "eşleşmedi" sayar ve uyarı olayı üretir.
- [ ] Relay `/metrics` (Prometheus): istek, gecikme, kod, engelleme nedeni,
      aktif ajan, tünel sayısı.

**Testler:** politika paketi tablo testleri (IPv4/IPv6, CIDR, XFF güveni,
sliding window sınırları); entegrasyon: engellenen IP'nin isteği ajana
ulaşmaz ve ajan AccessEvent alır; rate limit 429 + Retry-After; politika
relay restart sonrası ve ajan çevrimdışıyken uygulanır; sync versiyon
uyuşmazlığı tespiti; global vs route politikası önceliği.

**DoD**
- [ ] SPEC başarı ölçütü (tünel): IP engelle + rate limit koy → engellenen
      isteklerin relay'de kesildiği ajandaki olay akışında (CLI `latchd events
      --follow` ile) görülüyor.

**Karar noktaları:** global/route politika birleştirme sırası (öneri:
global deny → route deny → route allow → global allow → varsayılan izin);
rate limit bellek sınırı.

---

## Faz 6 — Yerel mod: gömülü Caddy + aynı politikaların ajanda uygulanması

**Amaç:** Aynı `latchd` binary'nin public IP'li bir sunucuda, Caddy
config'i göstermeden, TLS'li reverse proxy olması.

**Teslimatlar**
- [ ] `latchd/internal/local`: model → Caddy JSON üretici (HTTP app,
      otomatik TLS/ACME, HTTP→HTTPS, reverse_proxy, path bazlı yönlendirme,
      header kuralları, WebSocket, `tls: custom` için sertifika dosyası,
      `tls: off`); golden test dosyaları.
- [ ] Caddy'yi kütüphane olarak gömme (`caddy.Run`/`caddy.Load`), yalnızca
      gereken modüller derlenir; Caddy loglarını slog'a köprüleme.
- [ ] Özel Caddy HTTP handler modülü `latch_policy`: `/policy` motorunu
      sarar, AccessEvent üretir (kaynak=local).
- [ ] Sıfır kesintili uygulama: doğrula → etkilenen route'lar → `caddy.Load`;
      hata durumunda önceki config korunur, Caddy hatası alan düzeyine
      (`FieldError`) eşlenir, daemon asla düşmez.
- [ ] Upstream health checker (her iki mod için; ajan tarafında çalışır):
      aralık, zaman aşımı, durum (`healthy/unhealthy/unknown`) API'de.
- [ ] `latchd` dinleme adresi/port çakışması tespiti ve anlaşılır hata.

**Testler:** golden JSON testleri; entegrasyon: rastgele portta Caddy
(internal/local CA) + yerel upstream → HTTPS istek; path yönlendirme; header
kuralı; IP engeli ve rate limit yerel modda (aynı policy testleri yeniden
kullanılır); hatalı config uygulanmaz, önceki çalışmaya devam eder; health
check durum geçişleri.

**DoD**
- [ ] CLI ile `route add --local --hostname site.example.com --target
      127.0.0.1:8080` → geçerli TLS'li site (test ortamında Pebble/internal CA).
- [ ] Tek politika tanımı iki yerde aynı sonucu veriyor (ortak test tablosu).

**Karar noktaları:** HTTP/3 varsayılan açık mı; Caddy veri dizini konumu.

---

## Faz 7 — Gözlemlenebilirlik

**Amaç:** Yerel ve relay kaynaklı tüm olayları tek yerden görülebilir,
sorgulanabilir ve ölçülebilir kılmak.

**Teslimatlar**
- [ ] `latchd/internal/events`: birleşik olay modeli (local + relay), halka
      tampon + SQLite olay deposu; saklama politikası (süre ve boyut); disk
      dolunca güvenli davranış (en eskiyi sil, uyarı olayı, asla çökme).
- [ ] Yapısal JSON log (slog) + dosya rotasyonu; log arama/filtre
      (zaman aralığı, route, IP, kod, neden, metin).
- [ ] `latchd /metrics` (Prometheus): istek/sn, gecikme histogramı, kod
      dağılımı, route başına trafik, tünel durumu ve yeniden bağlanma sayısı,
      politika engellemeleri, health check durumu.
- [ ] Pano toplulaştırmaları (sunucu tarafı): istek/sn, p50/p95/p99, kod
      dağılımı, route başına trafik, "şu an engellenenler", son 1 saat en çok
      engellenen IP'ler.
- [ ] Explain aracı: route + örnek istek (IP, path, header) → kuralların
      sırası, nerede çalışacağı (ajan/relay), sonuç; `/policy.Explain`
      üzerine.
- [ ] CLI: `latchd events --follow --route x`, `latchd explain`.

**Testler:** saklama/boyut sınırı; disk dolu simülasyonu; percentile
hesapları; explain tablo testleri; metrics endpoint'inde beklenen seriler.

**DoD**
- [ ] Tünel ve yerel istekler aynı akışta kaynak etiketiyle görülüyor.
- [ ] Prometheus ile scrape edilip örnek panel (`deploy/grafana/`) çalışıyor.

**Karar noktaları:** varsayılan saklama (öneri: 7 gün / 500 MB); olay
örnekleme eşiği yüksek trafikte.

---

## Faz 8 — Yönetim API'si (REST + WS) ve kimlik doğrulama

**Amaç:** UI'nin ihtiyaç duyduğu tam API'yi, güvenli ve belgelenmiş
şekilde tamamlamak.

**Teslimatlar**
- [ ] `docs/API.md`'deki tüm kaynaklar: routes, ip-policies, rate-limits,
      relays (token ile ekle, durum, sync durumu), config/versions (liste,
      diff, rollback), backup (export/import), events (sorgu), stats (pano
      verileri), explain, settings, system (sürüm, sağlık).
- [ ] Dry-run: `POST /api/config/validate` → alan hataları + etkilenen
      route'lar (UI "uygula" öncesi gösterir).
- [ ] Relay kurulum desteği: `POST /api/relays/setup-script` (install.sh
      komutu üretir), `POST /api/relays/dns-check`.
- [ ] WS `/api/ws`: canlı olaylar, durum değişiklikleri (tünel, health, sync),
      config versiyonu değişti bildirimi.
- [ ] Kimlik doğrulama: parola veya token → oturum çerezi; loopback dışına
      bağlanınca zorunlu (aksi halde daemon başlamaz ve nedenini söyler);
      giriş denemesi rate limit; CSRF koruması; `latchd auth set-password`.
- [ ] OpenAPI 3 dokümanı (`docs/openapi.yaml`) ve CLI'nin tamamen API
      üzerinden çalışması.

**Testler:** httptest ile her endpoint; auth zorunluluğu; CSRF; WS olay
akışı; validate → affected routes.

**DoD**
- [ ] OpenAPI ile kod eşleşiyor (CI'da kontrol).
- [ ] Loopback dışı dinleme parola olmadan reddediliyor.

**Karar noktaları:** oturum süresi; parola hash (argon2id öneri).

---

## Faz 9 — Web UI (latch-ui)

**Amaç:** SPEC'teki "2 dakikada HTTPS" deneyimini arayüzden sunmak.

**Teslimatlar**
- [ ] Vite + React + TS, TanStack Query, Tailwind, Recharts; i18n altyapısı
      (en varsayılan, tr); `go:embed` ile latchd içinde servis; dev'de proxy.
- [ ] Ekranlar (`docs/SCREENS.md`): İlk kurulum/giriş; Pano; Route listesi
      (durum rengi: health + tünel bağlantısı); **Route ekle** iki-soru akışı
      (Ne? → Nasıl? Tünel: relay seç, alt ad üret/özel alan; Yerel: alan adı
      + DNS kontrolü); Route detayı (header, path, health, TLS, politikalar);
      IP politikaları + "Bu IP geçer mi?"; Rate limit + canlı engellenenler;
      Relay'ler (bağlan: token yapıştır; "Relay'ini kur" sihirbazı: betik +
      DNS kontrolü; sync durumu ve "relay senkron değil" uyarısı); Canlı
      akış; Loglar; Config geçmişi/geri al; Yedek/geri yükle; Ayarlar;
      Explain.
- [ ] Uygula öncesi doğrulama ve etkilenen route listesi diyaloğu; alan
      düzeyinde hata gösterimi.
- [ ] Playwright: en az "route ekle (tünel)" akışı uçtan uca.

**Testler:** bileşen testleri (Vitest) kritik formlar için; Playwright CI'da.

**DoD**
- [ ] SPEC başarı ölçütü (tünel) arayüzden: relay'e bağlan → "3000 portunu
      paylaş" → HTTPS adres; IP engelle + rate limit → canlı akışta relay'de
      kesildiği görülüyor.
- [ ] Türkçe çeviri eksiksiz; eksik anahtar CI'da hata.

**Karar noktaları:** tasarım dili (öneri: sade, shadcn/ui benzeri);
koyu tema v1'de var mı.

---

## Faz 10 — Electron kabuğu

**Amaç:** Masaüstünde "kur ve çalıştır" deneyimi; iş mantığı yok.

**Teslimatlar**
- [ ] `/electron`: latchd binary'sini paketleyip başlatma/durdurma, pencere
      UI'yi `http://127.0.0.1:<port>` üzerinden yükler, tray menüsü (durum,
      aç, durdur, çıkış), otomatik güncelleme (electron-updater), port
      çakışmasında yeniden deneme, günlükleri açma.
- [ ] electron-builder konfigürasyonu: macOS (dmg, notarization notu),
      Windows (nsis), Linux (AppImage, deb).

**Testler:** daemon yaşam döngüsü birim testleri; manuel smoke listesi
`docs/RELEASE_CHECKLIST.md`.

**DoD**
- [ ] Üç platformda paket üretiliyor; Electron'da UI dışı hiç iş mantığı yok.

---

## Faz 11 — Kurulum, paketleme, dağıtım

**Amaç:** Tek komutla kurulum ve tekrarlanabilir sürümler.

**Teslimatlar**
- [ ] `latchd install` (systemd / launchd / Windows servisi), `uninstall`,
      `latchd upgrade`; `latch-relay install` cilası.
- [ ] Docker imajları (`latchd`, `latch-relay`; distroless, multi-arch) ve
      `deploy/docker-compose.*.yml` örnekleri.
- [ ] goreleaser: çapraz derleme, checksum, imzalama (cosign), GitHub
      Releases; `deploy/install.sh` sürüm seçimi.
- [ ] Sürüm uyumluluk matrisi: Hello müzakeresinde min/max protokol sürümü;
      uyumsuzlukta her iki tarafta net mesaj ve UI uyarısı.
- [ ] README hızlı başlangıç (iki senaryo), `docs/` içeriği güncel.

**Testler:** kurulum betiklerinin container içinde smoke testi (Debian,
Alpine); release pipeline tag'de çalışır.

**DoD**
- [ ] Temiz bir Linux VM'de README'yi izleyen biri 10 dakikada iki senaryoyu
      çalıştırabiliyor.

---

## Faz 12 — Sertleştirme, kabul testleri, v1.0

**Amaç:** SPEC başarı ölçütlerini kanıtlamak ve güvenlik gereksinimlerini
kapatmak.

**Teslimatlar**
- [ ] Güvenlik kontrol listesi (`docs/SECURITY.md` ile eşleştirme): token
      hash/iptal/kapsam, hostname ele geçirme, relay yönetim API'sinin kapalı
      olması, boyut/süre limitleri, slowloris, bağlantı sınırı, tüm iç
      trafikte TLS doğrulaması, XFF güven listesi.
- [ ] Protokol codec fuzz testleri; yük testi (`deploy/loadtest/`) ve sonuç
      notu.
- [ ] Kabul senaryoları (`docs/ACCEPTANCE.md`, betiklerle): (1) tünel
      senaryosu 2 dakika, (2) yerel senaryo, (3) ajan çevrimdışı → geri
      gelme; her biri gerçek VPS'te kaydedilmiş kanıtla.
- [ ] Hata mesajları gözden geçirme (kullanıcıya dönük her hata eylem
      öneriyor mu).
- [ ] `CHANGELOG.md` v1.0.0; `docs/ROADMAP_NEXT.md` (QUIC, dağıtık rate
      limit, WAF, çoklu kullanıcı, relay kümeleme — v1 kapsam dışı).

**DoD**
- [ ] SPEC "Başarı ölçütleri" üç madde de kanıtla geçildi.
- [ ] Açık güvenlik bulgusu yok; v1.0.0 etiketlendi.

---

## Kesişen kurallar (her fazda geçerli)

- Kod ve repo dokümanları İngilizce; UI metinleri i18n (en varsayılan, tr).
- Belirsizlikte varsayım uydurma: en küçük güvenli seçeneği uygula ve
  `NOTES.md`'ye yaz; kullanıcıyı etkileyen kararlar için sor.
- Her faz çalışır ve test edilebilir durumda biter; "yarım" özellik varsa
  bayrak arkasında ve NOTES'ta.
- Sertifika doğrulaması hiçbir modda atlanmaz; dev için CA dosyası.
- Faz kapsamı dışına çıkma; fark edilen iş `NOTES.md` → "Backlog" bölümüne.
- Her faz tek commit (gerekirse birkaç mantıksal commit), mesaj
  `phase N: ...`.

## Açık kararlar (Faz 0'da kapatılacak)

- [x] Go modül yolu ve lisans. → `github.com/kayaramazan/latch`, Apache-2.0
- [x] Protokol kodlaması (JSON vs protobuf). → uzunluk önekli JSON
- [x] UI paket yöneticisi. → pnpm 9
- [x] GeoIP veritabanı dağıtım yöntemi. → relay'de yerel dosya; ajan üzerinden aktarım v1+
- [x] İlk desteklenecek DNS sağlayıcı listesi. → Cloudflare, Route53, Hetzner, DigitalOcean + manuel TXT (SPEC listesi)
