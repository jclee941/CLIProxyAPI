# Shared browser

One Docker image, one container per Google Chrome profile (per account). Each container runs real Google Chrome stable (apt repo at dl.google.com) on Xvfb `:99` with x11vnc (`:5900`), noVNC/websockify (`:6080`) and a socat CDP proxy (`:9223` to Chrome's loopback `:9222`). Debian bookworm slim, amd64 only.

## Build (once)

```bash
docker build -t cpa-shared-browser:local deploy/shared-browser
# Optional exact Chrome version: --build-arg CHROME_VERSION=150.0.7871.46-1
```

## Data directory layout (one per account)

Each container gets its own host directory, mounted at `/config`. It must be private (mode `0700`, owned by the container uid):

```text
<data-dir>/
  chrome/                  Chrome user data dir (--user-data-dir, --password-store=basic)
    Local State            copied from the source user data dir
    <Profile Directory>/   original Chrome profile dir name, e.g. Default or "Profile 1"
  vnc.passwd               x11vnc -rfbauth password file (mode 0600)
```

Chrome starts with `--profile-directory="<Profile Directory>"` and `about:blank`, so only that profile is opened. Nothing is deleted from the volume; on start only Chrome's stale `Singleton*` lock files are removed so a profile moved from another host opens.

## Run (per profile)

```bash
SHARED_BROWSER_CONTAINER_NAME=shared-browser-<account> \
SHARED_BROWSER_PROFILE_DIRECTORY='Profile 1' \
SHARED_BROWSER_DATA_DIR=/abs/path/to/<account> \
SHARED_BROWSER_NOVNC_PORT=6081 \
SHARED_BROWSER_CDP_PORT=9231 \
deploy/shared-browser/run.sh
```

`run.sh` starts a new container and fails if the name already exists; it never removes containers.

| Variable | Default | Meaning |
| --- | --- | --- |
| `SHARED_BROWSER_CONTAINER_NAME` | required | Must match `shared-browser-<account>` |
| `SHARED_BROWSER_PROFILE_DIRECTORY` | required | `Default` or `Profile N`; must exist under `<data-dir>/chrome/` |
| `SHARED_BROWSER_DATA_DIR` | required | Absolute per-account data dir above |
| `SHARED_BROWSER_NOVNC_PORT` / `SHARED_BROWSER_CDP_PORT` | required | Host ports (unique per container) |
| `SHARED_BROWSER_IMAGE` | `cpa-shared-browser:local` | Image to run |
| `SHARED_BROWSER_BIND_IP` | `127.0.0.1` | Bind IP for noVNC (and raw VNC) |
| `SHARED_BROWSER_CDP_BIND_IP` | `127.0.0.1` | Bind IP for CDP; non-loopback also needs `SHARED_BROWSER_ALLOW_REMOTE_CDP=1` (CDP has no auth) |
| `SHARED_BROWSER_VNC_PORT` | unset | Publish raw VNC `:5900` on this host port |
| `SHARED_BROWSER_MEMORY` | `2g` | Memory limit (no CPU quota is set) |
| `SHARED_BROWSER_UID` / `SHARED_BROWSER_GID` | caller's | Container user; must own the data dir |
| `SHARED_BROWSER_SCREEN` | `1920x1080x24` | Xvfb geometry |

Containers use `--shm-size 1g`, `--restart unless-stopped`, `--init` and a healthcheck (CDP and noVNC). CDP rejects non-IP `Host` headers, so address it by IP.

## Stopped while CPA holds the account

A login captured from a profile leaves that profile and CPA holding the same Google session. Both rotate its cookies, so a running browser strands CPA on retired ones, and the account fails with `credential_identity_invalid` until it is captured again. Keep the container stopped. Start it only when CPA's credential for that account has already failed, capture the login, and stop it right after `login/complete` succeeds:

```bash
docker start shared-browser-<account>
# sign in if needed, capture with the login companion
docker stop shared-browser-<account>
```

A manually stopped container stays stopped across Docker and NAS restarts.

## Flow captcha browser

`shared-browser-flowcaptcha` is the one container that keeps running. It holds
no session CPA could lose: it stays signed out, or is signed in only to a
dedicated Google account that CPA never holds. flow2api's
`captcha_provider: browser` mints Flow's reCAPTCHA tokens in it over DevTools at
`captcha_base_url: http://shared-browser-flowcaptcha:9223`, on the core's Docker
network. Its data directory holds an empty `Default` profile:

```bash
DIR=/volume1/docker/shared-browser/flowcaptcha
mkdir -p "$DIR/chrome/Default" && chmod 700 "$DIR" "$DIR/chrome" "$DIR/chrome/Default"
docker run --rm --entrypoint x11vnc --user "$(id -u):$(id -g)" -v "$DIR:/config" <image> \
    -storepasswd <password> /config/vnc.passwd && chmod 600 "$DIR/vnc.passwd"
SHARED_BROWSER_CONTAINER_NAME=shared-browser-flowcaptcha \
SHARED_BROWSER_PROFILE_DIRECTORY=Default \
SHARED_BROWSER_DATA_DIR="$DIR" \
SHARED_BROWSER_NOVNC_PORT=6082 \
SHARED_BROWSER_CDP_PORT=9232 \
SHARED_BROWSER_IMAGE=<image> \
deploy/shared-browser/run.sh
docker network connect cliproxyapi_backend shared-browser-flowcaptcha
```

Never sign it in to a Gemini or Flow account CPA holds; two holders rotate the
same session. Flow's free `low` priority queue refuses signed-out tokens in busy
hours, so a dedicated account CPA never holds may be signed in through noVNC;
flow2api keeps the browser's cookie jar.
