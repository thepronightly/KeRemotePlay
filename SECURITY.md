# Security

**English** · [Türkçe](#güvenlik)

KeRemotePlay changes system settings with admin rights and opens your PC to a friend over
Tailscale, so security problems matter. Thank you for reporting them.

## Reporting a vulnerability

Please **do not open a public issue** for a security problem. Report it privately instead:
**Security** tab → **Report a vulnerability** on this repository
(<https://github.com/thepronightly/KeRemotePlay/security/advisories/new>).

Include what you found, how to reproduce it and which version you used. This is a hobby project
kept by one person, so there is no fixed response time, but reports are read and answered.

## Supported versions

Only the latest release gets fixes.

## Known limits

Some risks are part of how the app works and are documented rather than treated as bugs: the
connection code carries a Tailscale auth key, your friend sees your whole desktop and can use your
keyboard and mouse, and all Sunshine pairings are removed at each session. See
[Security and known limits](README.md#security-and-known-limits).

---

## Güvenlik

KeRemotePlay yönetici izniyle sistem ayarlarını değiştirir ve bilgisayarını Tailscale üzerinden
bir arkadaşına açar; bu yüzden güvenlik sorunları önemli. Bildirdiğin için teşekkürler.

**Güvenlik sorunu için herkese açık issue açma.** Bunun yerine bu repoda **Security** sekmesi →
**Report a vulnerability** ile özel olarak bildir
(<https://github.com/thepronightly/KeRemotePlay/security/advisories/new>). Ne bulduğunu, nasıl
tekrarlandığını ve hangi sürümü kullandığını yaz. Bu tek kişinin hobi projesi; kesin bir yanıt
süresi yok ama bildirimler okunur ve cevaplanır.

Sadece son sürüm düzeltme alır. Uygulamanın doğası gereği olan riskler (bağlantı kodundaki auth
key, arkadaşın masaüstünü görmesi ve klavyeni kullanabilmesi, Sunshine eşleşmelerinin silinmesi)
hata değil, belgelenmiş sınırdır: [Güvenlik ve bilinen sınırlar](README.tr.md#güvenlik-ve-bilinen-sınırlar).
