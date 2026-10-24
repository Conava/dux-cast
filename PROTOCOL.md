# cast-helper protocol

One JSON object per line in each direction. Clients send commands, the helper sends events. The
full state is pushed on every change, so a client never polls and keeps no state of its own.

## Connecting

- `cast-helper attach` is what a client runs. It bridges its stdin and stdout to the shared helper
  on `$XDG_RUNTIME_DIR/cast-helper.sock`, starting `cast-helper serve` first when none is running.
  It exits with 0 when its stdin closes and 1 when the helper went away. Clients can also connect
  to the socket directly.
- The shared helper sends every client a `state` event as soon as it connects, then every change.
  A cast keeps running when the client that started it disconnects; the helper exits a few seconds
  after the last client is gone and nothing is casting.
- Plain `cast-helper` speaks the same protocol on its own stdin and stdout for a single client,
  and stops its cast when stdin closes.
- Replies to `check`, `thumbnail` and `playlist`, and errors about a command itself (`busy`,
  `unsupportedLink`), go to the client that sent it. Everything else goes to all clients.

## Versions

`check` reports `protocol`. Changes are additive: commands and fields are added, not changed or
removed, so a client works with any helper whose `protocol` is at least the one it was written
for, and should suggest an update otherwise. Helpers that don't report it speak protocol 1.

| Protocol | Added |
|---|---|
| 1 | Everything below not marked otherwise |
| 2 | `serve` and `attach`, `hello`, `session.owner`, `title`/`artist`/`album`/`art` on items, `version` in `check` |

## Commands

```jsonc
{"cmd": "hello", "client": "dplayer", "name": "Dux Player"}   // 2: names this client for session.owner
{"cmd": "check"}
{"cmd": "discover", "on": true}                                // while a device list is on screen
{"cmd": "start", "device": "<id>", "audio": "tv|both|local", "width": 1920, "height": 1080, "fps": 30,
 "kbps": 0 /* auto */, "codec": "auto|h264|hevc", "delay": 200, "restoreToken": "…",
 "protocol": "chromecast|airplay" /* where a device has both */, "airplayPorts": "47840-47849"}
{"cmd": "pair", "pin": "1234"}                                 // AirPlay PIN or password
{"cmd": "play", "device": "<id>", "path": "/abs/movie.mkv" | "url": "https://…", "port": 47830,
 "title"?, "artist"?, "album"?, "art"?}
{"cmd": "queue", "device": "<id>", "items": [{"path": "…"} | {"url": "…"}, …], "index": 0,
 "interval": 8, "loop": false, "shrinkPhotos": true, "convertVideos": true, "port": 47830}
{"cmd": "pause"} {"cmd": "resume"} {"cmd": "seek", "position": 30} {"cmd": "subtitles", "on": false}
{"cmd": "next"} {"cmd": "previous"} {"cmd": "jump", "index": 3} {"cmd": "slideshow", "interval": 0}
{"cmd": "move", "device": "<id>"}                              // the same cast on another device
{"cmd": "thumbnail", "path": "/abs/video.mp4", "size": 256}    // a song's thumbnail is its cover
{"cmd": "playlist", "path": "/abs/list.m3u8"}                  // .m3u, .m3u8, .pls
{"cmd": "stop"}
{"cmd": "volume", "level": 0.4}                                // receiver volume, where supported
```

Queue items and `play` take optional metadata (protocol 2): `title`, `artist`, `album`, and `art`,
an http(s) link or an absolute path of a cover image. The device shows it, and the session reports
it instead of the file name or link. A song file's own tags fill in what isn't given. A playlist
file in `items` expands in place.

## Events

```jsonc
{"event": "state", "devices": [{"id", "name", "model", "protocol": "chromecast|airplay", "protocols": […]}],
 "session": null | {"kind": "mirror|media", "device", "name",
   "phase": "starting|authorizing|pairing|converting|casting", "since", "volume", "canVolume",
   "owner"?: {"id": "dplayer", "name": "Dux Player"},   // 2: the client that started it, after its hello
   "protocol"?: "airplay", "credential"?: "pin|password" /* while pairing */,
   // media only:
   "title", "progress", "playerState", "position", "positionAt", "duration", "rate",
   "subtitles", "subtitlesOn", "index", "interval", "paused", "loop",
   "queue": [{"title", "artist", "album", "art"? /* links only */, "kind": "image|video|audio|link|youtube",
              "status": "waiting|preparing|ready|failed", "progress", "error"?, "feature"?, "missing"?}]}}
{"event": "check", "protocol": 2, "version": "0.1.0", "distro": "arch|debian|fedora|opensuse|other",
 "missing": [required ids], "deps": [{"id", "package", "required", "present", "features": […]}]}
{"event": "restoreToken", "token": "…"}                      // keep it for the next mirroring start
{"event": "thumbnail", "path": "…", "file": "~/.cache/dms/cast/thumbs/<hash>.jpg" | ""}
{"event": "playlist", "path": "…", "items": [{"path"?, "url"?, "title"?}]}
{"event": "error", "code": "…", "detail": "…", "feature"?: "mirroring", "missing"?: ["gst-plugin-va"]}
{"event": "warning", "code": "soundOnBoth", "feature": "tvOnlySound", "missing": ["pipewire"]}
{"event": "warning", "code": "airplaySound"}                 // AirPlay sound stays on this computer
```

`detail` is English for logs. Clients show their own text for each `code`: `airplayFailed`,
`airplayNoMedia`, `avahi`, `busy`, `deviceLost`, `deviceNotFound`, `encoderMissing`,
`launchRefused`, `loadFailed`, `missingDependency`, `pipelineFailed`, `portalDenied`, `portInUse`,
`receiverStopped`, `serverUnreachable`, `unsupportedFile`, `unsupportedLink`, `youtubeFailed`,
`youtubeInQueue`.

## Behavior

- **Dependencies are optional at start.** Each feature checks its packages when used and fails with
  `missingDependency` naming them. Features: `discovery`, `mirroring`, `sound`, `tvOnlySound`,
  `files`, `mkv`, `convert`, `photos`, `heic`, `thumbnails`.
- **Queue:** a single `play` is a queue of one. The current item and the next video or next two
  photos are prepared ahead (served, remuxed, converted, shrunk). Photos advance on the interval,
  everything else when it ends. A YouTube link plays alone, in the device's YouTube app.
- **Devices:** a TV with Chromecast and AirPlay is one device, keeping its Chromecast id.
  Mirroring uses Chromecast unless `protocol` says AirPlay; files, links and shows always use
  Chromecast.
- **Position:** the receiver reports it only on changes. Count on from `positionAt` while playing.
- **MPRIS:** a cast shows up as `org.mpris.MediaPlayer2.cast`. File playback has play, pause,
  next, previous and seek; for mirroring, pause and stop end the cast.
- **Debugging:** `CAST_DEBUG=1` logs every Cast message and HTTP request. The shared helper logs to
  `~/.cache/dms/cast/serve.log`.
