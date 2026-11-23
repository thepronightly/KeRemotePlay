# KeRemotePlay

**English** · [Türkçe](README.tr.md)

One small Windows app that installs and runs the Sunshine + Tailscale + Moonlight trio for you.
Streaming is still done by Sunshine/Moonlight. KeRemotePlay takes care of the setup, the network,
the pairing and the "why does it stutter?" question.

It was made for **couch co-op over the internet**: you play a game like FIFA on your PC, your
friend joins from home with their own controller, and you talk on Discord.

The app speaks English and Turkish. It starts in your Windows display language; the **EN / TR**
button at the top right switches it, and the choice is remembered.

> **Version 0.9.7 (beta).** Written for a group of friends and used that way. Before using it, read
> **[Security and known limits](#security-and-known-limits)**: the connection code carries a key
> to your Tailscale network, and your friend sees your screen and can use your keyboard.
> If something goes wrong, look at the log in `%APPDATA%\KeRemotePlay\krp.log`.

## How it works

```
 Friend's PC (Client)                              Your PC (Host)
 ┌──────────────────────┐   Tailscale (WireGuard)  ┌──────────────────────┐
 │ KeRemotePlay         │ ◀──────────────────────▶ │ KeRemotePlay         │
 │  └ Moonlight         │   direct P2P, relay if   │  ├ Sunshine (stream) │
 │                      │   it has to              │  └ Pairing service   │
 └──────────────────────┘                          │     (port 47900)     │
                                                   └──────────────────────┘
```

1. The host logs into Tailscale once with their own (free) account and makes an **auth key**.
2. Each time the app opens it asks: Host or Client? Pressing Host starts the needed services in
   the background and makes a new **connection code** (`KRP1-…`). The code is not saved; it is new
   every time.
3. Your friend pastes the code. The app puts them on the host's Tailscale network and asks the host:
   *"friend-pc wants to connect to your PC. Allow it?"*
4. If the host says **Yes**, Moonlight is paired with a random PIN. **Nobody types a PIN.**
5. After that it is just **▶ Play**. When the session ends the pairing is removed; the next
   connection asks for permission again.

## Usage

You both open `KeRemotePlay.exe`. Nothing else needs to be installed first. Each time it opens, the
app asks whether this PC is the **Host** or the **Client**.

**The PC that runs the game (you):** press **Host**.
1. Missing programs (Sunshine, Tailscale, the ViGEmBus controller driver) are downloaded and
   installed, Sunshine is set up and a firewall rule is added. Windows asks for permission **once**
   for all of it.
2. *First time:* Tailscale opens in the browser; log in with Google/Microsoft/GitHub.
3. *First time:* the Tailscale keys page opens: **Generate auth key…** → **Reusable** →
   **Generate key** → **Copy**. KeRemotePlay takes the key from the clipboard by itself.
4. The connection code is ready and **already on the clipboard**. Paste it to your friend.

Later starts skip the setup steps, but the code is new every time.

**Your friend:** opens KeRemotePlay, presses **Client**, pastes the code (if it is on the clipboard
it appears by itself) → **Connect**. Moonlight and Tailscale are installed if needed (Windows asks
once), the app joins the host's network and asks the host for permission. Then **▶ Play**. Your
friend does not need a Tailscale account.

On the host the question stays open for 60 seconds; the window comes to the front, flashes in the
taskbar and plays a sound. After **No**, the same PC cannot ask again for 30 seconds.

### Ending a session

When the host presses **Ctrl+F4** or **End session**, the stream closes at once, the pairing is
removed and the code stops working; your friend can only connect again with a new code and a new
permission. Ctrl+F4 is only taken while a stream runs; at other times it works normally in other
programs. Closing KeRemotePlay or pressing **Back to start** also ends the session. When your friend
closes Moonlight, the session ends by itself.

KeRemotePlay must stay open on the host while you play.

### What closes when you go back to the start

Services only run while the Host or Client screen is open. When you press **Back to start** or close
the app:

- **Host:** the session ends, the pairing port (47900) closes, Sunshine stops.
- **Client:** the Moonlight stream closes.
- **Tailscale on both:** if you were on another Tailscale network before, it switches back to it;
  otherwise it disconnects. Tailscale itself stays installed; only its connection is closed.

Sunshine no longer starts with Windows. For this Windows asks for permission once on the first Host
start; after that starting and stopping it asks nothing.

**Note:** pressing Host removes **all** pairings in this PC's Sunshine, including other devices you
paired with Moonlight by hand.

If Windows Firewall also asks "allow access?", press **Allow**.

### Stream settings: resolution, FPS, bitrate

On your friend's screen under **Settings**. Each of the three can be left on **Auto** or set by hand:

| Setting | Options |
|---|---|
| Resolution | Auto, Same as my screen, 720p, 900p, 1080p, 1440p, 4K |
| FPS | Auto, 30, 60, 90, 120, 144 |
| Bitrate | Empty = Auto, or a number between 0.5 and 500 Mbps |

Whatever you pick by hand is used as it is. Only the parts left on Auto follow the line:

- If anything is on Auto, pressing Play pulls data from the host for 5 seconds to measure the real
  speed (a result is kept for 10 minutes). If everything is set by hand, nothing is measured.
- **Resolution Auto:** your screen's own resolution first, then 1440p → 1080p → 900p → 720p if the
  line cannot carry it.
- **FPS Auto:** 120 first on screens of 120 Hz and above, otherwise 60.
- **Bitrate Auto:** 75% of the measured speed, but no more than Moonlight's default for that
  resolution/fps.
- If a value you picked is more than the line can carry, the app does not stop you; it only warns
  that it may stutter.

The bottom of the screen shows which settings will be used. Example (everything on Auto):
14 Mbps → 1080p · 60 fps · 10.5 Mbps. 6 Mbps → 720p · 60 fps · 4.5 Mbps.

## Audio, microphones and Discord

- **Microphones never go into the stream.** Sunshine only records what the host plays to its
  speakers (Windows "loopback"); it does not record microphones. The PC version of Moonlight does
  not send a microphone either. Voice chat stays entirely in Discord.
- **The real risk is the host's Discord audio getting into the stream.** If Discord on the host
  plays to the "Default" output, your friend hears everyone on Discord, themselves included, as a
  delayed echo in the stream. To avoid it:
  1. KeRemotePlay always opens the stream in "do not play audio on the host" mode. In this mode
     Sunshine moves Windows' default output to a virtual speaker (Steam Streaming Speakers) for the
     length of the stream.
  2. On the host, in Discord → User Settings → **Voice & Video** → **Output Device**, pick your
     headphones or speakers instead of "Default". Then Discord stays out of the stream and you still
     hear Discord.
  3. The virtual speaker needs **Steam installed** on the host (it does not need to be running;
     Sunshine installs the driver itself) or VB-Cable. If it is missing, the host screen warns you.
- **Couch co-op: "Let me hear the game too"** (on by default). During the stream the game audio
  goes to the virtual speaker, so normally the player at the host would not hear the game.
  KeRemotePlay takes the audio from the virtual speaker and also plays it in your headphones.
  Discord does not take this path, so it does not reach your friend. The added delay is a few tens
  of milliseconds. Pick the device under **Settings** on the host screen; "Auto" uses the default
  device from before the stream. The KeRemotePlay window must stay open on the host for this.

```
 Game ──▶ (default output = virtual speaker) ──┬──▶ Sunshine ──▶ your friend
                                               └──▶ KeRemotePlay ──▶ your headphones
 Discord ──▶ your headphones (fixed)               (not in the stream)
```

## Your friend's controllers: any controller works (Xbox, PlayStation, DInput/generic)

A controller reaches the game in two steps:

1. **Moonlight on your friend's PC must recognize the controller.** Moonlight reads controllers
   through SDL and only sends the ones whose layout (which button is A, which axis is the left
   stick) it knows. Xbox, PS4/PS5, Switch Pro and thousands of known DInput pads are already
   recognized. An unknown generic pad is **silently ignored**.
   - KeRemotePlay uses Moonlight's own SDL2.dll to list controllers **the way Moonlight sees
     them**: "Moonlight will use it" or "Moonlight does not recognize this controller".
   - For an unknown pad, **Set it up**: the screen asks for 21 steps such as "Bottom button (Xbox A
     · PS Cross)" and "Push the left stick RIGHT". Skip buttons your pad does not have. At least A, B
     and the left stick are needed. When done, **Save**.
   - The saved layout is given to Moonlight when you press Play (Moonlight also saves it). **Test**
     shows live that each button maps correctly.
   - While a stream runs KeRemotePlay does not touch the controllers; Moonlight has them.
2. **On the host the controller appears as an Xbox controller.** KeRemotePlay sets Sunshine up so
   that whatever controller your friend uses, an XInput (Xbox 360) controller appears on the host
   and every game understands it. Buttons map to their Xbox counterparts (Cross = A, Circle = B,
   Square = X, Triangle = Y); the touchpad and motion sensors are not used.

Notes: a PS3 controller needs an extra driver on Windows (e.g. DsHidMini); once installed it shows
up in the list. Controller database: [SDL_GameControllerDB](https://github.com/mdqinc/SDL_GameControllerDB) (zlib license).

## Couch co-op tips

- **Picture:** by default Sunshine does not touch your monitor's resolution and streams your
  screen as it is. Auto resolution on your friend's side never goes above the host's screen; if
  the host is 1080p, asking for 1440p only wastes bitrate.
- **Player order:** Windows gives a newly connected controller the lowest free number. If you want
  your friend to be player 1, let them connect first, then turn on your controller. In most sports
  games (FIFA/EA FC) each controller can also pick a side on the team select screen.
- Your friend's controller should be connected to their PC before the Moonlight stream starts.
- **Who starts the game:** you do. Your friend connects with "Desktop" and sees your screen.
- **Latency:** your friend gets about 30–60 ms of extra latency (network + encoding). Games like
  FIFA usually play fine. In competitive online matches it can be noticeable.

## Smoothness: what the signs on screen mean

| Sign | Meaning | What to do |
|---|---|---|
| **Direct connection** | No middleman between the two PCs | This is the best case |
| **Via relay (fra etc.)** | Traffic goes through a Tailscale server abroad | Wait a few seconds; it usually switches to direct. If it stays, the router/CGNAT blocks it (see below) |
| **Host: Wi-Fi** | The host is on wireless | The number 1 cause of stutter; plug in a cable |
| **Ping** | Under 40 ms is good, over 80 ms is noticeable | — |
| **Line X Mbps** | The real speed from the host to you | Under 8 Mbps the reason is almost always the host's upload speed; Auto lowers the settings to match |

**If the relay stays:** with Tailscale's *peer relay* feature (available on the free plan) you can
make a cheap VPS near you, or a PC with a fixed IP, the middleman. KeRemotePlay does not set this up
for you yet (see *Ideas* below).

## Testing without a friend

- **The host side** can be tried alone: seeing the invite code is enough.
- **For the client** you need a second Windows PC (a laptop, for example). Being in the same house
  is fine.
- On the same home network the connection looks much better than it really is. For a realistic
  test, connect the laptop to your **phone's mobile data (hotspot)**. Mobile networks are behind
  CGNAT, so this is close to your friend connecting from their home. Mind the data: a 10 Mbps stream
  uses about 4.5 GB per hour.

## Security and known limits

Read this before using it. KeRemotePlay opens your screen and control of your PC to your friend and
changes system settings with admin rights. All of it is below.

### The connection code is a key to your network

Your code (`KRP1-…`) contains your Tailscale **auth key**. Anyone who gets the code can **join your
Tailscale network** for as long as the key is valid:

- The secret part of the code changes every time you start as Host, and every connection asks for
  your permission. So nobody can connect to your **game** unnoticed with an old code.
- But the auth key stays the same (Reusable). Someone with an old code can still join your
  **network**, and with Tailscale's default settings reach **other devices** on it (NAS, other PCs,
  printer).
- Only send the code privately, to people you trust. Do not paste it into a public channel.

There are two ways to prevent this. Picking one is **strongly recommended**:

1. **Make a separate Tailscale account for KeRemotePlay (simplest).** For example with a separate
   Google or GitHub account. Then that network holds nothing but the gaming PC. If you have your
   own Tailscale network, KeRemotePlay does not break it: when you leave the Host screen it switches
   back to your previous network.
2. **Safe mode:** if you use your own network, use the ACL below so friends' devices can only
   reach the game ports.

If the code got into the wrong hands: in the Tailscale admin, **Revoke** the key under
**Settings → Keys**, **Remove** devices you do not know under **Machines**, then enter a new key in
KeRemotePlay.

### Your friend sees your screen and can use your keyboard and mouse

- The stream shows your **whole desktop**: open windows, notifications, Discord messages included.
  Close anything you do not want to show before the stream.
- Your friend's controller, keyboard and mouse go to your PC. This is on purpose: some games need
  keyboard and mouse. It means that during the stream they can do anything with your account.
- **Ctrl+F4** or **End session** cuts the stream at once, removes the pairing and makes the code
  invalid.

### What it changes with admin rights

On the first Host and Client setup Windows asks for permission once. With it:

- winget installs **Sunshine, Tailscale, ViGEmBus** (host) and **Moonlight, Tailscale** (client).
- **Sunshine settings** change (`sunshine.conf`: controller driver `vigembus`, controller type
  `x360`) and a user name/password is set for Sunshine's web panel.
- **Sunshine's service is set to "manual start"** and your user gets permission to start and stop
  it. Sunshine **no longer starts with Windows**; it only runs while the Host screen is open. Keep
  this in mind if you use Sunshine for other things and want it always on.
- **Tailscale** on the host is set to "unattended" mode (so it can stay connected while you are
  signed out).
- Two inbound **Windows Firewall** rules are added, for the pairing service and for Sunshine's
  ports. Both are open **only to Tailscale addresses** (100.64.0.0/10); they cannot be reached from
  your home network or the internet.

### Your Sunshine pairings are removed

Every Host start and every session end removes **all** pairings in Sunshine. This way a paired
Moonlight cannot connect again without your permission. The side effect: if you paired devices
like a TV or tablet with Moonlight by hand, you have to pair them again every time.

### What is open on the network

- **47900/TCP (pairing service):** open only while the Host screen is open. It only answers
  Tailscale addresses that know the secret in the code. It closes when you go back to the start.
- **Sunshine ports (47984–48010):** while Sunshine runs. The firewall rule opens them to the
  Tailscale network only. Who inside the Tailscale network can reach them is up to the ACL (Safe
  mode).
- If the app is killed (Task Manager, power cut), Sunshine and Tailscale may stay on. KeRemotePlay
  stops a leftover Sunshine the next time it starts.

### Other

- **Your friends' devices show up in your Tailscale account.** You can see and remove them in the
  admin page.
- **An auth key expires after 90 days.** Friends already connected are not affected. For a new
  friend, make a new key and update it in the app with **Change the invite key**.
- **A device key may expire after 180 days.** Your friend then has to connect again. To avoid it,
  pick *Disable key expiry* for that device in the Tailscale admin page (Safe mode does this by
  itself).
- Settings are stored in **plain text** in `%APPDATA%\KeRemotePlay\config.json` (Sunshine password
  and auth key included). Do not share this file; look inside the log (`krp.log`) before sending it
  to anyone.
- **The exe is not signed.** Windows SmartScreen may say "unrecognized app". Only download the exe
  from this repository's **Releases** page (built by GitHub Actions, with its SHA256 next to it), or build it yourself from source (see *Development*).
- Tailscale's free plan is for non-commercial use.
- Windows only. MIT licensed, **with no warranty of any kind** (see [LICENSE](LICENSE)).

### Safe mode (friends can only reach the game)

In the Tailscale admin → **Access controls** page, add the following. Put the host's Tailscale
address in place of `100.x.y.z`:

```json
{
  "tagOwners": { "tag:krp-friend": ["autogroup:admin"] },
  "acls": [
    { "action": "accept", "src": ["autogroup:member"], "dst": ["*:*"] },
    { "action": "accept", "src": ["tag:krp-friend"], "dst": ["100.x.y.z:47900,47984-48010"] }
  ]
}
```

Then pick **Tags → tag:krp-friend** when you make the auth key. Devices that join with this key can
only reach the host's game ports, and their keys do not expire.

## Third-party software

KeRemotePlay does not bundle any of these programs. Each is installed by winget from its own
official source and comes with its own license. KeRemotePlay only calls them from the command line
or through their own web APIs.

| Program | Used for | License / note |
|---|---|---|
| [Sunshine](https://github.com/LizardByte/Sunshine) | Streaming on the host | GPL-3.0 |
| [Moonlight](https://moonlight-stream.org/) | Opening the stream on the client | GPL-3.0. For controller detection, Moonlight's own SDL library is loaded (zlib). |
| [Tailscale](https://tailscale.com/) | Private network between the two PCs | The client core is open source (BSD-3). The service is under Tailscale's terms; the free plan is for personal, non-commercial use. Plan limits can change over time. |
| [ViGEmBus](https://github.com/nefarius/ViGEmBus) | Your friend's controller appears as an Xbox controller on the host | BSD-3. The project is **retired** and no longer updated. It works for now; if a future Windows update breaks it, a fix may not come. |

Included in this repository: [SDL_GameControllerDB](https://github.com/mdqinc/SDL_GameControllerDB)
(zlib, license in `assets/`). Go libraries: go-webview2, go-wca, go-ole (MIT) and golang.org/x/sys
(BSD-3).

KeRemotePlay is not affiliated with any of these projects; their names are only used to say what
is used.

## Troubleshooting

- **"winget not found"**: update *App Installer* from the Microsoft Store, or install the program by
  hand from the page that opens. The app notices the installation by itself.
- **"Could not reach KeRemotePlay on the host"**: the app must be open on the host.
- **Pairing timed out**: if the Moonlight window shows an error, close it and press **▶ Play**
  again.
- **Coming from 0.8.x:** if the old controller order task is installed, the host screen shows
  "Remove"; it asks for permission once and also re-enables a controller left disabled.
- Log file: `%APPDATA%\KeRemotePlay\krp.log`

## Development

Needs Go 1.24+. On Windows, `build.bat` runs the tests and builds `dist\KeRemotePlay.exe`. It also
builds and tests on Linux/Mac (`go test ./...`); there the tools are looked up on PATH as
`tailscale`, `moonlight` and `sunshine`, so fake tools can stand in for them.

| File | Purpose |
|---|---|
| `main.go`, `server.go`, `web/index.html` | App window (WebView2) and interface |
| `actions.go` | Host/Client state, connecting and pairing flow |
| `agent.go` | Pairing service on the host (Tailscale addresses only, with the secret) |
| `sunshine.go` | Sunshine API: password check, PIN approval (old and 2026.10+ API) |
| `tailscale.go` | Status, ping (direct/relay), login |
| `moonlight.go` | Moonlight CLI: pair / list / stream |
| `setup.go` | winget installs, admin-only work (one UAC prompt) |
| `invite.go` | Invite code |
| `prepare.go` | One-button flows: host setup (install → login → key → code), client install |
| `clip_windows.go` | Clipboard: taking the invite code/key from it, putting the code on it |
| `legacy.go`, `cfgmgr_windows.go` | Cleanup of 0.8.x's removed controller order task |
| `audio.go`, `audio_windows.go` | Couch co-op audio relay (virtual speaker → host headphones, WASAPI) |
| `clientpads.go`, `clientpads_windows.go` | Friend's controllers: detection with Moonlight's SDL2.dll, setup wizard, test |
| `speed.go` | Speed test and automatic quality plan (matches Moonlight's bitrate formula) |
| `i18n.go`, `lang_*.go` | Turkish/English. Every text is written in place as `L("Türkçe", "English")`. |

### Ideas

Things that might come later. These are ideas, not promises.

- **Virtual display** matching the friend's screen resolution.
- **Single exe:** Tailscale built into the app (tsnet), so there is no separate install or auth key step.
- **Peer relay:** when the two PCs cannot connect directly, route the stream through a nearby PC
  or cheap server of your own instead of Tailscale's relay servers abroad.
