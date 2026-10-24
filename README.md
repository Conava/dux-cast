# dux-cast

`cast-helper` casts the screen, files, music and links from Linux to Chromecast, Google TV and
AirPlay devices. It is the backend of the [Cast plugin](https://github.com/Conava/dux-plugins/tree/master/Cast)
for Dux Shell, and other apps can use it too: one shared helper runs per user, and every app
connected to it sees and controls the same cast.

- Screen mirroring with sound: Chromecast (real time, GPU encoded) or AirPlay through
  [doubletake](https://github.com/omarroth/doubletake).
- Files, music, photos, playlists and links on Chromecast devices. What a device can't play is
  converted on the GPU first.
- Devices are found with Avahi. MPRIS shows what's playing.

## Install

Arch (AUR):

```bash
yay -S cast-helper
```

Or download the release build (x86_64 or aarch64, built by GitHub Actions from the tagged source).
It is only installed if it matches the release's checksums:

```bash
(mkdir -p ~/.local/share/dms/cast/bin && cd ~/.local/share/dms/cast/bin \
  && dms dl https://github.com/Conava/dux-cast/releases/latest/download/cast-helper-$(uname -m) \
    --timeout 300 -o cast-helper-$(uname -m) \
  && dms dl https://github.com/Conava/dux-cast/releases/latest/download/SHA256SUMS \
    | grep " cast-helper-$(uname -m)$" | sha256sum -c \
  && mv cast-helper-$(uname -m) cast-helper && chmod +x cast-helper)
```

Or build it (Go 1.25 or newer, no C compiler):

```bash
git clone https://github.com/Conava/dux-cast.git && cd dux-cast
CGO_ENABLED=0 go build -o ~/.local/share/dms/cast/bin/cast-helper .
```

It needs Avahi and GStreamer at run time, loaded when a feature is used. `{"cmd": "check"}` reports
every package, what it's for and whether it's there; the Cast plugin's README lists them per
distribution.

## Use

```bash
cast-helper attach   # what an app runs: stdin and stdout go to the shared helper
cast-helper serve    # the shared helper; attach starts it when needed
cast-helper          # a private helper on stdin and stdout, for one app
cast-helper version
```

Commands and events are JSON lines, described in [PROTOCOL.md](PROTOCOL.md).

## Network and files

Apart from the cast devices on the local network, the helper only contacts youtube.com, when a
YouTube link is played, and the server of a direct link, to ask for its media
type when the address doesn't show it. Files are served to the cast device only, from a port that
only answers its address.

| Path | Contents |
|---|---|
| `~/.cache/dms/cast` | Converted files while they play, thumbnails, the shared helper's log |
| `~/.local/share/dms/cast` | AirPlay pairings, a downloaded helper or doubletake |
| `$XDG_RUNTIME_DIR` | `cast-helper.sock` and `cast-helper.lock`, `dms-cast/` during an AirPlay cast |

## License

MIT, see [LICENSE](LICENSE).
