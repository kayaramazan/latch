# Proje: Latch — Yerel reverse proxy + self-hosted tünel, arayüzden yönetilen

## Amaç
İki kullanım senaryosunu tek üründe birleştiren açık kaynak bir sistem:
1. Yerel mod: Public IP'si olan bir sunucuda klasik reverse proxy
   (alan adı → upstream), terminal konfigürasyonu olmadan arayüzden yönetim.
2. Tünel modu: NAT arkasındaki bir bilgisayardaki portu, kullanıcının kendi
   VPS'inde çalışan relay üzerinden internete açma ("3000 portumu paylaş").
Öncelik sırası: güvenilirlik, basitlik, gözlemlenebilirlik. Hedef deneyim:
kur, bir port veya alan adı seç, 2 dakikada geçerli HTTPS ile yayında ol.

## Bileşenler (zorunlu mimari)
### latchd — ajan/daemon (Go, tek binary)
- Arayüzsüz çalışır; systemd/launchd/Windows servisi ve Docker ile kurulur.
- Yerel mod için Caddy v2'yi kütüphane olarak gömer (ACME, HTTP/1.1-2-3,
  sıcak reload hazır gelir). Kullanıcı Caddy config'i asla görmez; bizim veri
  modelimizden Caddy JSON'u türetilir.
- Tünel modu için bir veya birden fazla relay'e dışarı doğru kalıcı bağlantı
  açar, kopunca üstel geri çekilmeyle yeniden bağlanır.
- Yönetim API'si (REST + WebSocket) sunar ve latch-ui'yi statik olarak servis
  eder. Varsayılan dinleme 127.0.0.1; web'e açmak bilinçli bir ayardır ve
  token/şifre + oturum zorunludur.
- Konfigürasyon SQLite'ta, her değişiklik versiyonlu ve geri alınabilir.

### latch-relay — relay sunucusu (Go, tek binary, self-hosted)
- Kullanıcı kendi VPS'ine kurar. Kurulum sihirbazı: alan adı, wildcard DNS
  (*.relay.alan.com → VPS IP) ve e-posta ister; wildcard sertifikayı ACME
  DNS-01 ile (Cloudflare, Route53, Hetzner, DigitalOcean sağlayıcıları; aksi
  halde manuel TXT) ya da alt alan başına TLS-ALPN-01 ile alır.
- 443'te TLS sonlandırır, SNI/Host'a göre hangi tünele gideceğini bulur.
- Ajan bağlantılarını ayrı bir portta (varsayılan 7443, TLS) kabul eder.
  Kimlik doğrulama: relay'de üretilen, kapsamı sınırlı ajan token'ları
  (hangi alt adlara / özel alan adlarına izin var).
- Kural uygulama noktası tünel modu için BURASIDIR: IP allow/deny, GeoIP,
  rate limit, temel header kuralları relay'de çalışır; engellenen trafik
  ajana hiç inmez. Kurallar ajandan relay'e API ile itilir, relay bunları
  kalıcı tutar (ajan çevrimdışıyken de bilinir).
- Erişim olaylarını ilgili ajana gerçek zamanlı geri akıtır (WebSocket)
  ki kullanıcı tek arayüzden her şeyi görsün. Relay kendi metriklerini de
  /metrics ile verir.
- Özel alan adı: kullanıcı kendi alanını CNAME ile relay'e yönlendirir;
  relay doğrulama sonrası sertifikayı on-demand ACME ile alır.
- Ajan çevrimdışıyken anlaşılır bir 502/503 sayfası döner.
- Çok kiracılı değil: tek sahipli, ama birden fazla ajan ve cihaz bağlanabilir.

### Tünel protokolü
- Taşıma: TLS üzerinde tek TCP bağlantısı, hashicorp/yamux ile çoklama.
  (QUIC/HTTP3 ikinci aşama.) Alternatif değerlendirme: mevcut
  frp/rathole/chisel protokollerini uyarlamak yerine basit, belgelenmiş
  kendi mesaj şemamızı tanımla; PROTOCOL.md'ye yaz.
- Mesajlar: Hello (versiyon, token, yetenekler), RouteAnnounce (hangi
  alt adlar/alanlar → hangi yerel hedef), Stream open/close, Ping/Pong,
  PolicySync, AccessEvent.
- HTTP ve TCP tünelleri desteklenir (TCP için relay'de ayrılmış port).
  WebSocket şeffaf geçer.
- İstemci IP'si ajana PROXY protocol v2 veya X-Forwarded-For (güvenilen
  relay listesi ile) olarak iletilir.

### latch-ui — tek web uygulaması (React + TypeScript)
- latchd tarafından sunulur; Electron bu sayfayı yükleyen ince kabuktur
  (pencere, tray, daemon başlat/durdur, otomatik güncelleme). Electron'da
  iş mantığı YOK.
- Route ekleme akışı iki soru sorar: "Neyi yayınlayacaksın?" (yerel port
  veya host:port) ve "Nasıl erişilsin?" → Tünel (relay seç, alt ad üret
  veya özel alan gir) ya da Yerel (bu makinenin public IP'sine DNS
  yönlendirilmiş alan adı). Diğer her şey varsayılanla gelir.
- Kurulum sihirbazları: "Relay'ini kur" (VPS'e tek komutla kurulum betiği
  üretir, DNS kontrolü yapar) ve "Relay'e bağlan" (token yapıştır).

## Ortak veri modeli
Route { id, name, enabled, target (host:port), exposure: local|tunnel,
hostnames[], relayId?, tls: auto|custom|off, headers[], healthCheck,
ipPolicyId?, rateLimitIds[], createdAt, version }
IP politikaları ve rate limit kuralları route'tan bağımsız nesnelerdir;
birden fazla route'a bağlanabilir, global olarak da uygulanabilir.
Yerel modda politikalar latchd içinde (Caddy middleware olarak), tünel
modunda relay'de uygulanır; tek tanım, iki uygulama noktası.

## Özellikler — MVP
### Route yönetimi
Alan adı/alt ad → hedef, otomatik TLS, HTTP→HTTPS, WebSocket, header
ekle/sil, path bazlı yönlendirme, aç/kapa, upstream health check ve
UI'da durum rengi. Tünel modunda ajan bağlantı durumu da gösterilir.

### IP kısıtlama
Allow/deny, tekil IP ve CIDR, ülke bazlı (GeoLite2/DB-IP dosyası yüklenir),
reddetme davranışı seçilebilir (403/404/bağlantı kes), "Bu IP geçer mi?"
test aracı. Güvenilir istemci IP tespiti: XFF yalnızca güvenilen proxy
listesinden kabul edilir.

### Rate limiter
Anahtar: IP, header, path veya birleşimi; sliding window N/süre; route ve
global düzeyde; aşımda 429 + Retry-After veya gecikme; canlı "şu an
engellenenler" ve son 1 saat en çok engellenenler.

### Gözlemlenebilirlik
Canlı istek akışı (zaman, route, kaynak: yerel/relay adı, IP, metod,
path, kod, süre, hedef, engellenme nedeni); panolar (istek/sn,
p50/p95/p99, kod dağılımı, route başına trafik, tünel bağlantı
sağlığı/yeniden bağlanma sayısı); yapısal JSON log + filtre/arama;
saklama politikası ve disk dolunca güvenli davranış; /metrics Prometheus
(ajan ve relay ayrı ayrı); "explain" aracı: bir istek için hangi kuralın
nerede (ajan mı relay mi) çalışacağını ve sonucunu gösterir.

### Güvenilirlik ve işletim
Değişiklik önce doğrulanır, etkilenen route'lar listelenir, atomik ve
sıfır kesintili uygulanır; hatalı config daemon'ı asla düşürmez, önceki
çalışan config korunur, hata alan düzeyinde açıklanır; relay'e politika
itme başarısızsa UI'da açıkça "relay senkron değil" uyarısı; yedek al/
geri yükle; ajan ve relay sürüm uyumsuzluğunda net mesaj; tek komutla
kurulum: `latchd install`, `latch-relay install`; Docker imajları;
Electron paketleri (macOS/Windows/Linux).

## Güvenlik gereksinimleri
- Relay ajan token'larını yalnızca hash'lenmiş saklar; token iptal
  edilebilir ve kapsamı (izinli alan adları) sınırlıdır.
- Relay yönetim API'si varsayılan kapalı; yalnızca ajan tünel bağlantısı
  üzerinden yönetilir.
- Alt ad çakışması ve ele geçirme engellenir (bir ad bir token'a bağlı).
- Relay'de istek başına boyut/süre limitleri, yavaş-istemci (slowloris)
  koruması, bağlantı sayısı sınırı.
- Tüm bileşenler arası trafik TLS; sertifika doğrulaması atlanmaz.

## Teknoloji
Go 1.22+, Caddy v2 (gömülü, yalnızca ajanda), hashicorp/yamux, SQLite
(modernc.org/sqlite), slog, Prometheus client, ACME için Caddy'nin certmagic
kütüphanesi (relay'de de). React + TS + Vite, TanStack Query, Tailwind,
Recharts. Electron + electron-builder. Testler: Go birim ve entegrasyon
testleri (tünel üzerinden uçtan uca istek, IP engeli relay'de, rate limit,
yeniden bağlanma, politika senkronu); Playwright ile en az bir UI akışı.

## Çalışma biçimi
1. Önce yazılı olarak çıkar ve onay bekle: repo yapısı (monorepo:
   /latchd, /latch-relay, /latch-ui, /electron, /proto), veri modeli,
   PROTOCOL.md taslağı, REST/WS API sözleşmesi, ekran listesi.
2. Sıra: ortak model + versiyonlama → tünel protokolü ve relay çekirdeği
   → ajan tünel istemcisi (ilk uçtan uca: yerel port relay üzerinden
   HTTPS'te açılır) → relay'de IP/rate limit → yerel mod (Caddy) ve aynı
   politikaların ajanda uygulanması → gözlemlenebilirlik → web UI →
   Electron → kurulum/paketleme.
3. Her adımda çalışan, test edilebilir durum bırak; CHANGELOG'a yaz.
4. Belirsizlikte varsayım uydurma; en küçük güvenli seçeneği uygula,
   kararı NOTES.md'ye yaz.
5. Kod ve dokümantasyon İngilizce; UI metinleri i18n (İngilizce varsayılan,
   Türkçe dahil).

## Kapsam dışı (v1)
WASM/Lua eklentiler, dağıtık rate limit (Redis), WAF, cache, Kubernetes
keşfi, çoklu kullanıcı/rol, relay kümelenmesi, QUIC tünel taşıması,
barındırılan (SaaS) relay hizmeti.

## Başarı ölçütleri
- Tünel: kullanıcı VPS'ine relay'i tek komutla kurar, bilgisayarında
  arayüzden "3000 portunu paylaş" der, 2 dakika içinde geçerli HTTPS'li
  bir adres alır; bir IP'yi engeller ve rate limit koyar, engellenen
  isteklerin relay'de kesildiğini canlı akışta görür.
- Yerel: VPS'te alan adı → upstream ekler, terminalde hiçbir dosya
  düzenlemeden TLS'li site çalışır.
- Ajan çevrimdışı olup geri geldiğinde tünel kendiliğinden toparlanır,
  kullanıcı hiçbir şey yapmaz.
