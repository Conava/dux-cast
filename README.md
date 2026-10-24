# dux-cast

`cast-helper` casts the screen, files, music and links from Linux to Chromecast, Google TV and
AirPlay devices. It's the backend of the [Cast plugin](https://github.com/Conava/dux-plugins/tree/master/Cast)
for Dux Shell, and other apps can use it too. One helper runs per user, and every app connected to
it sees and controls the same cast.

- Screen mirroring with sound: Chromecast (real time, GPU encoded) or AirPlay through
  [doubletake](https://github.com/omarroth/doubletake).
- Files, music, photos, playlists and links on Chromecast devices.
- Incompatible files are converted on-demand with GPU acceleration.
- Devices are found with Avahi. MPRIS is supported to control playing media.

## Install

### 1. Dependencies

Arch users may skip this step by using the AUR package listed below. Installing all optional dependencies is recommended.

Debian / Ubuntu:

```bash
sudo apt install avahi-daemon gstreamer1.0-plugins-base gstreamer1.0-plugins-bad gstreamer1.0-pipewire \
  gstreamer1.0-plugins-good gstreamer1.0-libav pipewire-bin libheif-examples
```

Fedora:

```bash
sudo dnf install avahi gstreamer1-plugins-base gstreamer1-plugins-bad-free pipewire-gstreamer \
  gstreamer1-plugins-good gstreamer1-plugin-libav pipewire-utils libheif-tools
```

openSUSE:

```bash
sudo zypper install avahi gstreamer-plugins-base gstreamer-plugins-bad gstreamer-plugin-pipewire \
  gstreamer-plugins-good gstreamer-plugins-libav pipewire-tools heif-examples
```

Arch, when building from source:

```bash
sudo pacman -S --needed avahi gstreamer gst-plugins-base gst-plugins-bad gst-plugin-pipewire gst-plugin-va \
  gst-plugins-good gst-libav libheif
```

The first line each are the required dependencies, the second line is recommended for more formats, 
sound on the cast device only, HEIC photos. 

To start Avahi:

```bash
sudo systemctl enable --now avahi-daemon
```

Mirroring needs VA-API H.264 or HEVC encoding. Fedora and openSUSE ship drivers without it, so get
them from RPM Fusion or Packman. `{"cmd": "check"}` lists every package, what it's for and whether
it's installed; the Cast plugin shows that list in its settings.

### 2. Install cast-helper

Arch (AUR):

```bash
paru -S cast-helper
```

Or download the release build (x86_64 or aarch64, built automatically on release):

```bash
(mkdir -p ~/.local/share/dms/cast/bin && cd ~/.local/share/dms/cast/bin \
  && dms dl https://github.com/Conava/dux-cast/releases/latest/download/cast-helper-$(uname -m) \
    --timeout 300 -o cast-helper-$(uname -m) \
  && dms dl https://github.com/Conava/dux-cast/releases/latest/download/SHA256SUMS \
    | grep " cast-helper-$(uname -m)$" | sha256sum -c \
  && mv cast-helper-$(uname -m) cast-helper && chmod +x cast-helper)
```

Build from source (Go 1.25 or newer):

```bash
git clone https://github.com/Conava/dux-cast.git && cd dux-cast
CGO_ENABLED=0 go build -o ~/.local/share/dms/cast/bin/cast-helper .
```

### 3. doubletake (AirPlay only)

doubletake is an independent AirPlay sender, not part of this project. Review it before installing.
cast-helper is tested with 0.5.0 and finds it on `PATH`, in `~/.local/share/dms/cast/bin` or in
`~/go/bin`.

Arch (AUR):

```bash
paru -S doubletake-bin
```

Other distributions, from its releases (x86_64 shown, use `doubletake-arm64` on aarch64):

```bash
dms dl https://github.com/omarroth/doubletake/releases/download/v0.5.0/doubletake \
  --timeout 300 -o ~/.local/share/dms/cast/bin/doubletake \
  && chmod +x ~/.local/share/dms/cast/bin/doubletake
```

doubletake also runs `gst-launch-1.0` and `pactl`. Install `gstreamer1.0-tools` and
`pulseaudio-utils` on Debian / Ubuntu, `pulseaudio-utils` on Fedora, `gstreamer-utils` and
`pulseaudio-utils` on openSUSE, `libpulse` on Arch.

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
YouTube link is played, and the server of a direct link, to ask for its media type when the address
doesn't show it. Files are served to the cast device only.

| Path | Contents |
|---|---|
| `~/.cache/dms/cast` | Converted files while they play, thumbnails, the shared helper's log |
| `~/.local/share/dms/cast` | AirPlay pairings, a downloaded helper or doubletake |
| `$XDG_RUNTIME_DIR` | `cast-helper.sock` and `cast-helper.lock`, `dms-cast/` during an AirPlay cast |

## License

MIT, see [LICENSE](LICENSE).
