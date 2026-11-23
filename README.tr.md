# KeRemotePlay

[English](README.md) · **Türkçe**

Sunshine + Tailscale + Moonlight üçlüsünü tek bir küçük uygulamadan kuran ve yöneten araç.
Yayın motoru Sunshine/Moonlight olarak kalıyor. KeRemotePlay kurulumu, ağı, eşleştirmeyi ve
"neden takılıyor?" sorusunun cevabını üstleniyor.

Program Türkçe ve İngilizce. İlk açılışta Windows'un diline göre başlar; sağ üstteki
**EN / TR** düğmesiyle değiştirilir ve seçim hatırlanır.

> **Sürüm 0.9.7 (beta).** Bir arkadaş grubu için yazıldı ve öyle kullanılıyor. Kullanmadan önce
> **[Güvenlik ve bilinen sınırlar](#güvenlik-ve-bilinen-sınırlar)** bölümünü oku: bağlantı kodu
> Tailscale ağına giriş anahtarı taşır, arkadaşın ekranını görür ve klavyeni kullanabilir.
> Bir şey takılırsa `%APPDATA%\KeRemotePlay\krp.log` kaydına bak.

## Nasıl çalışıyor

```
 Arkadaşın PC'si (Client)                         Senin PC'n (Host)
 ┌──────────────────────┐   Tailscale (WireGuard)  ┌──────────────────────┐
 │ KeRemotePlay         │ ◀──────────────────────▶ │ KeRemotePlay         │
 │  └ Moonlight         │   doğrudan P2P, olmazsa  │  ├ Sunshine (yayın)  │
 │                      │   relay                  │  └ Eşleştirme servisi│
 └──────────────────────┘                          │     (port 47900)     │
                                                   └──────────────────────┘
```

1. Host, Tailscale'e bir kez kendi (ücretsiz) hesabıyla giriş yapar ve bir **auth key** üretir.
2. Program her açıldığında sorar: Host mu, Client mı? Host'a basınca gereken servisler arkada
   açılır ve yeni bir **bağlantı kodu** (`KRP1-…`) üretilir. Kod kaydedilmez, her seferinde değişir.
3. Arkadaşın kodu yapıştırır. Uygulama onu host'un Tailscale ağına sokar ve host'a sorar:
   *"arkadas-pc bilgisayarınıza bağlanmak istiyor. İzin verilsin mi?"*
4. Host **Evet** derse Moonlight rastgele bir PIN'le eşleştirilir. **Kimse PIN yazmaz.**
5. Sonrası sadece **▶ Oyna**. Oturum bitince eşleşme silinir; yeni bağlantı yine izin ister.

## Kullanım

İkiniz de `KeRemotePlay.exe`'yi açarsınız. Başka bir şey kurmanız gerekmez. Program her açılışta
**Host** mu **Client** mı olacağını sorar.

**Oyunun çalıştığı PC (sen):** **Host**'a bas.
1. Eksik programlar (Sunshine, Tailscale, kol sürücüsü ViGEmBus) indirilip kurulur, Sunshine
   ayarlanır, güvenlik duvarı izni eklenir. Hepsi için Windows **bir kez** izin sorar.
2. *İlk sefer:* Tarayıcıda Tailscale açılır; Google/Microsoft/GitHub ile giriş yap.
3. *İlk sefer:* Tailscale anahtar sayfası açılır: **Generate auth key…** → **Reusable** →
   **Generate key** → **Copy**. KeRemotePlay anahtarı panodan kendisi alır.
4. Bağlantı kodu hazır ve **panoya kopyalanmış** olur. Arkadaşına yapıştırıp gönder.

Sonraki açılışlarda kurulum adımları atlanır, ama kod her seferinde yenidir.

**Arkadaşın:** KeRemotePlay'i açar, **Client**'a basar, kodu yapıştırır (kod panodaysa kendiliğinden
gelir) → **Bağlan**. Moonlight ve Tailscale gerekiyorsa kurulur (Windows bir kez izin sorar), host'un
ağına girilir ve host'a izin sorulur. Sonra **▶ Oyna**. Tailscale hesabı açması gerekmez.

Host'ta soru 60 saniye açık kalır; pencere öne çıkar, görev çubuğunda yanıp söner ve ses çalar.
**Hayır** denirse aynı bilgisayar 30 saniye yeniden soramaz.

### Oturumu bitirmek

Host **Ctrl+F4**'e ya da **Oturumu bitir**'e basınca yayın hemen kapanır, eşleşme silinir ve kod
geçersiz olur; arkadaşın ancak yeni kodla ve yeniden izinle bağlanabilir. Ctrl+F4 sadece yayın
sırasında ayrılır, başka zaman programlarda normal çalışır. KeRemotePlay'i kapatmak ya da
**Başa dön** de oturumu bitirir. Arkadaşın Moonlight'ı kapatınca oturum kendiliğinden biter.

Host'ta KeRemotePlay oyun boyunca açık kalmalı.

### Başa dönünce kapananlar

Servisler sadece Host ya da Client ekranındayken açık kalır. **Başa dön**'e basınca ya da program
kapanınca:

- **Host:** oturum biter, eşleştirme portu (47900) kapanır, Sunshine durur.
- **Client:** Moonlight yayını kapanır.
- **İkisinde de Tailscale:** Önceden başka bir ağdaysan o ağa döner; değilse bağlantısı kesilir.
  Tailscale'in kendisi kurulu kalır, sadece bağlantısı kapanır.

Sunshine artık Windows'la birlikte açılmaz. Bunun için ilk Host'ta Windows bir kez izin sorar;
sonraki açılıp kapanmalarda sormaz.

**Dikkat:** Host'a basıldığında bu bilgisayardaki Sunshine'ın **bütün** eşleşmeleri silinir;
Moonlight'la elle eşleştirdiğin başka cihazlar varsa onlar da.

Windows Güvenlik Duvarı ayrıca "erişime izin verilsin mi?" diye sorarsa **İzin ver** de.

### Yayın ayarları: çözünürlük, FPS, bitrate

Arkadaşın ekranında **Ayarlar** altında. Üçü de ayrı ayrı ya **Otomatik** bırakılır ya da elle seçilir:

| Ayar | Seçenekler |
|---|---|
| Çözünürlük | Otomatik, Ekranımla aynı, 720p, 900p, 1080p, 1440p, 4K |
| FPS | Otomatik, 30, 60, 90, 120, 144 |
| Bitrate | Boş = Otomatik, ya da 0,5–500 Mbps arası bir sayı |

Elle seçtiğin her şey olduğu gibi kullanılır. Sadece Otomatik bıraktıkların hatta göre seçilir:

- Otomatik kısım varsa Oyna'ya basınca host'tan bu bilgisayara 5 saniye veri çekilip gerçek
  hız ölçülür (ölçüm 10 dakika geçerli). Her şey elle seçiliyse ölçüm yapılmaz.
- **Çözünürlük Otomatik:** önce ekranının kendi çözünürlüğü, hat kaldırmazsa 1440p → 1080p → 900p → 720p.
- **FPS Otomatik:** 120 Hz ve üstü ekranda önce 120, olmazsa 60.
- **Bitrate Otomatik:** ölçülen hızın %75'i, ama Moonlight'ın o çözünürlük/fps için varsayılanını geçmez.
- Elle seçtiğin değer hattın üstündeyse uygulama engellemez, sadece "takılma olabilir" diye uyarır.

Ekranın altında hangi ayarla açılacağı yazar. Otomatik seçilen kısımlar **oto** etiketiyle gösterilir.
Örnek (her şey Otomatik): 14 Mbps → 1080p · 60 fps · 10,5 Mbps. 6 Mbps → 720p · 60 fps · 4,5 Mbps.

## Ses, mikrofon ve Discord

- **Mikrofonlar yayına hiç girmez.** Sunshine sadece host'un hoparlöre çaldığı sesi kaydeder
  (Windows "loopback"), mikrofon kaydetmez. Moonlight'ın PC sürümü de mikrofon göndermez.
  Sesli konuşma tamamen Discord'da kalır. Bunu iki programın kaynak kodundan kontrol ettim.
- **Asıl risk, host'taki Discord'un çaldığı sesin yayına karışması.** Discord host'ta "Varsayılan"
  çıkışa çalıyorsa arkadaşın Discord'daki herkesi, kendi sesi dahil, yayında gecikmeli yankı
  olarak duyar. Bunu önlemek için:
  1. KeRemotePlay yayını her zaman "ses host'ta çalmasın" modunda açar. Bu modda Sunshine,
     yayın süresince Windows'un varsayılan çıkışını sanal bir hoparlöre (Steam Streaming Speakers) alır.
  2. Host'ta Discord → Kullanıcı Ayarları → **Ses ve Görüntü** → **Çıkış Cihazı**'nı
     "Varsayılan" yerine kulaklığına ya da hoparlörüne sabitle. Böylece Discord yayına girmez,
     sen de Discord'u duymaya devam edersin.
  3. Sanal hoparlör için host'ta **Steam kurulu olmalı** (açık olması gerekmez; sürücüyü
     Sunshine kendisi kurar) ya da VB-Cable kurulmalı. Eksikse host ekranında uyarı çıkar.
- **Couch co-op (FIFA vb.): "Oyunun sesini ben de duyayım"** (varsayılan olarak açık). Yayın
  sırasında oyunun sesi sanal hoparlöre gittiği için normalde host'taki oyuncu oyunu duymaz.
  KeRemotePlay sanal hoparlördeki sesi alıp senin kulaklığına da çalar. Discord bu yoldan
  geçmediği için arkadaşına gitmez. Ek gecikme birkaç on milisaniye. Hangi cihaza çalınacağını
  host ekranındaki **Ayarlar**'dan seçebilirsin; "Otomatik" yayından önceki varsayılan cihazı kullanır.
  Bunun için host'ta KeRemotePlay penceresi açık kalmalı.

```
 Oyun ──▶ (varsayılan çıkış = sanal hoparlör) ──┬──▶ Sunshine ──▶ arkadaşın
                                                └──▶ KeRemotePlay ──▶ senin kulaklığın
 Discord ──▶ senin kulaklığın (sabit)                (yayına girmez)
```

## Arkadaşının kolları: her kol çalışsın (Xbox, PlayStation, DInput/jenerik)

Bir kolun oyuna gitmesi iki adımda oluyor:

1. **Arkadaşının bilgisayarında Moonlight kolu tanımalı.** Moonlight kolları SDL ile okur ve sadece
   düzenini (hangi tuş A, hangi eksen sol analog) bildiği kolları gönderir. Xbox, PS4/PS5, Switch Pro
   ve binlerce bilinen DInput kolu zaten tanınır. Tanınmayan jenerik bir kol **sessizce yok sayılır**.
   - KeRemotePlay, Moonlight'ın kendi SDL2.dll dosyasını kullanarak kolları **Moonlight'ın gördüğü
     gibi** listeler: "Moonlight kullanacak" ya da "Moonlight bu kolu tanımıyor".
   - Tanınmayan kol için **Kolu tanıt**: ekran sırayla "Alttaki tuş (Xbox A · PS Çarpı)",
     "Sol analoğu SAĞA it" gibi 21 adım sorar. Kolunda olmayan tuşu "atla" ile geçersin. En az A, B ve
     sol analog gerekir. Bittiğinde **Kaydet**.
   - Kaydedilen tanım, Oyna'ya basınca Moonlight'a verilir (Moonlight kendi ayarlarına da kaydeder).
     **Test et** ile tuşların doğru karşılığa gittiğini canlı görebilirsin.
   - Yayın sürerken KeRemotePlay kollara dokunmaz; kollar tamamen Moonlight'ta olur.
2. **Host'ta kol Xbox kolu olarak oluşur.** KeRemotePlay Sunshine'ı buna göre ayarlar: arkadaşın hangi
   kolu kullanırsa kullansın host'ta XInput (Xbox 360) kolu belirir ve her oyun onu tanır. Tuşlar Xbox
   karşılıklarına gelir (Çarpı = A, Daire = B, Kare = X, Üçgen = Y); touchpad ve hareket sensörü kullanılmaz.

Notlar: PS3 kolu Windows'ta ek sürücü (ör. DsHidMini) ister; sürücü kurulunca listede görünür.
Kol veritabanı: [SDL_GameControllerDB](https://github.com/mdqinc/SDL_GameControllerDB) (zlib lisansı).

## Couch co-op ipuçları

- **Görüntü:** Sunshine varsayılan ayarda senin monitörünün çözünürlüğüne dokunmaz, ekranını
  olduğu gibi yayınlar. Arkadaşın tarafındaki Otomatik çözünürlük de host ekranından yükseğe
  çıkmaz; host 1080p ise 1440p istemek sadece bitrate harcar.
- **Oyuncu sırası:** Windows yeni bağlanan kola en küçük boş numarayı verir. Arkadaşın 1. oyuncu
  olsun istiyorsan önce o bağlansın, sonra sen kolunu aç. Çoğu spor oyununda (FIFA/EA FC gibi) takım
  seçme ekranında her kol tarafını ayrıca seçebilir.
- Arkadaşının kolu, Moonlight yayını başlamadan önce bilgisayarına bağlı olsun.
- **Oyunu kim açar:** Sen açarsın. Arkadaşın "Desktop" ile bağlanır ve ekranını görür.
- **Gecikme:** Arkadaşın tarafında ~30–60 ms ek gecikme olur (ağ + kodlama). FIFA gibi oyunlarda
  genelde rahat oynanır. Rekabetçi online maçlarda fark edilebilir.

## Akıcılık: ekrandaki işaretler ne anlama geliyor

| İşaret | Anlamı | Ne yapmalı |
|---|---|---|
| **Doğrudan bağlantı** | İki PC arasında aracı yok | En iyi durum bu |
| **Relay üzerinden (fra vb.)** | Trafik Tailscale'in yurtdışındaki sunucusundan geçiyor | Birkaç saniye bekleyip **Yenile**'ye bas, çoğu zaman doğrudana geçer. Kalıcıysa modem/CGNAT engelliyordur (aşağıya bak) |
| **Host: Wi-Fi** | Host kablosuz bağlı | Takılmaların 1 numaralı sebebi, kablo tak |
| **Ping** | 40 ms altı iyi, 80 ms üstü hissedilir | — |
| **Hat X Mbps** | Host'tan sana gerçekten gelen hız | 8 Mbps altıysa sebep neredeyse her zaman host'un upload'udur; Otomatik ayar buna göre düşürür |

**Relay kalıcıysa:** Tailscale'in *peer relay* özelliğiyle (ücretsiz planda var) Türkiye'de
ucuz bir VPS'i ya da sabit IP'si olan bir PC'yi aracı yapabilirsin. KeRemotePlay bunu henüz
kendisi kurmuyor (aşağıda *Fikirler*'e bak).

## Arkadaş olmadan nasıl test edilir

- **Host tarafı** tek başına denenebilir: davet kodunu görmek yeterli.
- **Client için** ikinci bir Windows bilgisayar (örneğin bir laptop) lazım. Aynı evde olması sorun değil.
- Aynı ev ağında bağlantı gerçekte olduğundan çok iyi görünür. Gerçekçi bir test için laptop'u
  **telefonun mobil verisine (hotspot)** bağla. Mobil ağlar CGNAT arkasında olduğu için bu,
  arkadaşının evinden bağlanmaya yakın bir durum yaratır. Dikkat: 10 Mbps'lik yayın saatte yaklaşık 4,5 GB veri harcar.

## Güvenlik ve bilinen sınırlar

Kullanmadan önce oku. KeRemotePlay arkadaşına bilgisayarının ekranını ve kontrolünü açar,
yönetici izniyle sistem ayarlarına dokunur. Hepsi aşağıda.

### Bağlantı kodu ağına giriş anahtarıdır

Kodun (`KRP1-…`) içinde Tailscale **auth key**'in var. Kodu eline geçiren herkes, anahtar
geçerli olduğu sürece **Tailscale ağına katılabilir**:

- Her Host açılışında koddaki gizli anahtar değişir ve her bağlantı senin iznini ister. Yani eski
  kodla kimse habersizce **oyuna** bağlanamaz.
- Ama auth key aynı kalır (Reusable). Eski kodu bilen biri yine de **ağına** girebilir ve
  varsayılan Tailscale ayarıyla ağındaki **diğer cihazlara** (NAS, öbür PC'ler, yazıcı) ulaşabilir.
- Kodu sadece güvendiğin kişiye, özelden gönder. Herkese açık kanala yapıştırma.

Bunu önlemenin iki yolu var. Birini seçmen **şiddetle önerilir**:

1. **KeRemotePlay için ayrı bir Tailscale hesabı aç (en basiti).** Örneğin ayrı bir Google ya da
   GitHub hesabıyla. O ağda oyun bilgisayarından başka bir şey olmaz. Kendi Tailscale ağın varsa
   KeRemotePlay onu bozmaz: Host ekranından çıkınca önceki ağına geri döner.
2. **Güvenli mod:** Kendi ağını kullanacaksan aşağıdaki ACL ile arkadaş cihazlarının sadece
   oyun portlarına erişmesini sağla.

Kod yanlış ellere geçtiyse: Tailscale admin → **Settings → Keys**'ten anahtarı **Revoke** et,
**Machines**'ten tanımadığın cihazları **Remove** et, sonra KeRemotePlay'de yeni anahtar gir.

### Arkadaşın ekranını görür, klavyeni ve fareni kullanabilir

- Yayın **masaüstünün tamamını** gösterir: açık pencereler, bildirimler, Discord mesajları dahil.
  Göstermek istemediğin şeyleri yayından önce kapat.
- Arkadaşının kolu, klavyesi ve faresi senin bilgisayarına gider. Bu bilerek böyle: bazı oyunlar
  klavye ve fare istiyor. Yani yayın sırasında senin hesabınla her şeyi yapabilir.
- **Ctrl+F4** ya da **Oturumu bitir** yayını anında keser, eşleşmeyi siler, kodu geçersiz yapar.

### Yönetici izniyle değiştirdikleri

İlk Host ve Client kurulumunda Windows bir kez izin sorar. O izinle:

- winget ile **Sunshine, Tailscale, ViGEmBus** (host) ve **Moonlight, Tailscale** (client) kurulur.
- **Sunshine ayarları** değişir (`sunshine.conf`: kol sürücüsü `vigembus`, kol tipi `x360`)
  ve Sunshine web arayüzü için kullanıcı adı/şifre atanır.
- **Sunshine servisi "elle başlat"** yapılır ve senin kullanıcına başlatma/durdurma izni verilir.
  Sunshine artık **Windows'la birlikte açılmaz**, sadece Host ekranındayken çalışır. Sunshine'ı
  başka amaçla hep açık kullanıyorsan bunu bil.
- **Tailscale** host'ta "unattended" moda alınır (oturum kapalıyken de bağlı kalabilsin diye).
- **Güvenlik Duvarı**'na eşleştirme servisi ve Sunshine portları için iki gelen trafik kuralı
  eklenir. İkisi de **sadece Tailscale adreslerine** (100.64.0.0/10) açıktır; ev ağından ya da
  internetten erişilemez.

### Sunshine eşleşmelerin silinir

Her Host açılışında ve her oturum sonunda Sunshine'daki **bütün** eşleşmeler silinir. Böylece
eşleşmiş bir Moonlight, senin iznin olmadan tekrar bağlanamaz. Ama bunun yan etkisi var:
Moonlight'la elle eşleştirdiğin TV, tablet gibi cihazların varsa onları her seferinde yeniden
eşleştirmen gerekir.

### Ağda açık olanlar

- **47900/TCP (eşleştirme servisi):** Sadece Host ekranındayken açık. Sadece Tailscale
  adreslerinden ve koddaki gizli anahtarla cevap verir. Başa dönünce kapanır.
- **Sunshine portları (47984–48010):** Sunshine açıkken. Güvenlik duvarı kuralı bunları sadece
  Tailscale ağına açar. Tailscale ağının içinde kimin erişeceğini ise ACL belirler (Güvenli mod).
- Program zorla kapatılırsa (Görev Yöneticisi, elektrik kesintisi) Sunshine ve Tailscale açık
  kalabilir. KeRemotePlay bir sonraki açılışta kalmış Sunshine'ı kapatır.

### Diğer

- **Arkadaşların cihazları senin Tailscale hesabında görünür.** Admin panelinden görüp silebilirsin.
- **Auth key 90 gün sonra dolar.** Zaten bağlanmış arkadaşlar etkilenmez. Yeni arkadaş için
  yeni anahtar üretip uygulamada **Davet anahtarını değiştir** ile güncelle.
- **Cihaz anahtarı 180 gün sonra dolabilir.** O zaman arkadaşın yeniden bağlanmak zorunda kalır.
  Bunun olmaması için Tailscale admin panelinde o cihaz için *Disable key expiry* seç
  (Güvenli mod'da bu kendiliğinden olur).
- Ayarlar `%APPDATA%\KeRemotePlay\config.json` dosyasında **düz metin** olarak durur
  (Sunshine şifresi ve auth key dahil). Bu dosyayı kimseyle paylaşma; kayıt dosyası
  (`krp.log`) gönderirken içine bakmak da iyi olur.
- **Exe imzasız.** Windows SmartScreen "tanınmayan uygulama" diyebilir. Exe'yi sadece bu reponun
  **Releases** sayfasından indir (GitHub Actions derler, SHA256 yanında durur) ya da kaynaktan kendin derle (aşağıda *Geliştirme*).
- Ücretsiz Tailscale planı ticari olmayan kullanım içindir.
- Program sadece Windows için. MIT lisanslı, **hiçbir garanti verilmez** (bkz. [LICENSE](LICENSE)).

### Güvenli mod (arkadaşlar sadece oyuna erişsin)

Tailscale admin → **Access controls** sayfasında şunu ekle. `100.x.y.z` yerine host'un
Tailscale adresini yaz:

```json
{
  "tagOwners": { "tag:krp-friend": ["autogroup:admin"] },
  "acls": [
    { "action": "accept", "src": ["autogroup:member"], "dst": ["*:*"] },
    { "action": "accept", "src": ["tag:krp-friend"], "dst": ["100.x.y.z:47900,47984-48010"] }
  ]
}
```

Sonra auth key üretirken **Tags → tag:krp-friend** seç. Bu anahtarla katılan cihazlar sadece
host'un oyun portlarına erişir ve anahtar süreleri dolmaz.

## Kullanılan yazılımlar

KeRemotePlay bu programların hiçbirini kendi içinde taşımaz. Hepsi winget ile kendi resmi
kaynağından kurulur ve kendi lisansıyla gelir. KeRemotePlay onları sadece komut satırından ya da
kendi web API'lerinden çağırır.

| Program | Ne için | Lisans / not |
|---|---|---|
| [Sunshine](https://github.com/LizardByte/Sunshine) | Host'ta yayın | GPL-3.0 |
| [Moonlight](https://moonlight-stream.org/) | Client'ta yayını açar | GPL-3.0. Kol tanıma için Moonlight'ın kendi SDL kütüphanesi yüklenir (zlib). |
| [Tailscale](https://tailscale.com/) | İki bilgisayar arasındaki özel ağ | İstemcinin çekirdeği açık kaynak (BSD-3). Hizmetin kendisi Tailscale'in şartlarına tabi; ücretsiz plan kişisel ve ticari olmayan kullanım içindir. Plan sınırları zamanla değişebilir. |
| [ViGEmBus](https://github.com/nefarius/ViGEmBus) | Arkadaşın kolu host'ta Xbox kolu olarak görünsün | BSD-3. Proje **emekliye ayrıldı**, artık güncellenmiyor. Şimdilik çalışıyor; ileride bir Windows güncellemesi bozarsa düzeltme gelmeyebilir. |

Repoya dahil olanlar: [SDL_GameControllerDB](https://github.com/mdqinc/SDL_GameControllerDB)
(zlib, lisansı `assets/` içinde). Go kütüphaneleri: go-webview2, go-wca, go-ole (MIT) ve
golang.org/x/sys (BSD-3).

KeRemotePlay bu projelerin hiçbiriyle bağlantılı değildir; adları sadece neyin kullanıldığını
anlatmak için geçer.

## Sorun giderme

- **"winget bulunamadı"**: Microsoft Store'dan *Uygulama Yükleyici*'yi (App Installer)
  güncelle ya da açılan sayfadan programı elle kur. Uygulama kurulumu kendisi algılar.
- **"Host'taki KeRemotePlay'e ulaşılamadı"**: Host'ta uygulama açık olmalı.
- **Eşleştirme zaman aşımı**: Moonlight penceresi hata gösterdiyse kapat ve tekrar **▶ Oyna**'ya bas.
- **0.8.x'ten geçenler:** Eski kol sıralama görevi kuruluysa host ekranında "Kaldır" çıkar; bir kez
  izin ister, kapalı kalmış kol varsa onu da açar.
- Kayıt dosyası: `%APPDATA%\KeRemotePlay\krp.log`

## Geliştirme

Gereken: Go 1.24+. Windows'ta `build.bat` testleri çalıştırıp `dist\KeRemotePlay.exe`'yi üretir.
Linux/Mac'te de derlenip test edilebilir (`go test ./...`); orada araçlar PATH'teki
`tailscale`, `moonlight`, `sunshine` adlarıyla aranır, böylece sahte araçlarla deneme yapılabilir.

| Dosya | Görevi |
|---|---|
| `main.go`, `server.go`, `web/index.html` | Uygulama penceresi (WebView2) ve arayüz |
| `actions.go` | Host/Client durumu, bağlanma ve eşleştirme akışı |
| `agent.go` | Host'taki eşleştirme servisi (yalnız Tailscale adreslerinden, gizli anahtarla) |
| `sunshine.go` | Sunshine API: şifre kontrolü, PIN onayı (eski ve 2026.10+ API) |
| `tailscale.go` | Durum, ping (doğrudan/relay), giriş |
| `moonlight.go` | Moonlight CLI: pair / list / stream |
| `setup.go` | winget kurulumları, yönetici gerektiren işler (tek UAC) |
| `invite.go` | Davet kodu |
| `prepare.go` | Tek düğmeli akışlar: host hazırlığı (kurulum → giriş → anahtar → kod), client kurulumu |
| `clip_windows.go` | Pano: davet kodunu/anahtarı panodan alma, kodu panoya koyma |
| `legacy.go`, `cfgmgr_windows.go` | 0.8.x'in kaldırılan kol sıralama görevinin temizliği |
| `audio.go`, `audio_windows.go` | Couch co-op ses aktarımı (sanal hoparlör → host kulaklığı, WASAPI) |
| `clientpads.go`, `clientpads_windows.go` | Arkadaşın kolları: Moonlight'ın SDL2.dll'iyle tanıma, "Kolu tanıt" sihirbazı, test |
| `speed.go` | Hız ölçümü ve otomatik kalite planı (Moonlight'ın bitrate formülüyle uyumlu) |
| `i18n.go`, `lang_*.go` | Türkçe/İngilizce. Her metin yerinde `L("Türkçe", "English")` olarak yazılır. |

### Fikirler

İleride gelebilecek şeyler. Bunlar fikir, söz değil.

- **Sanal ekran:** arkadaşın ekran çözünürlüğüne uyan bir ekran.
- **Tek exe:** Tailscale uygulamanın içine gömülür (tsnet); ayrı kurulum ve auth key adımı kalkar.
- **Peer relay:** iki bilgisayar doğrudan bağlanamadığında yayın, Tailscale'in yurtdışındaki
  sunucuları yerine yakındaki kendi PC'nden ya da ucuz bir sunucudan geçer.
