FROM debian:bookworm-slim@sha256:88200866dfff7ea7f5cbcb6ec7c8a701889efe6fe859fe64d6990e4b07ea4171

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates curl dbus-x11 fonts-noto-cjk fonts-noto-color-emoji \
        novnc openbox websockify x11-utils x11vnc xdotool xvfb \
    && curl --fail --silent --show-error --location \
        https://dl.google.com/linux/chrome/deb/pool/main/g/google-chrome-stable/google-chrome-stable_153.0.8010.36-1_amd64.deb \
        --output /tmp/google-chrome.deb \
    && printf '%s  %s\n' 9bb44e33031c2f2857cf36b4343051a12f93058e4b781e3c76313df87f6c8d32 /tmp/google-chrome.deb | sha256sum --check \
    && apt-get install -y --no-install-recommends /tmp/google-chrome.deb \
    && rm /tmp/google-chrome.deb \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --create-home --uid 1000 --shell /bin/bash desktop \
    && ln -s vnc.html /usr/share/novnc/index.html

COPY start-desktop.sh /usr/local/bin/start-desktop
RUN chmod 0755 /usr/local/bin/start-desktop

USER 1000:1000
ENV HOME=/home/desktop DISPLAY=:99 DBUS_SESSION_BUS_ADDRESS=unix:path=/tmp/desktop-dbus
WORKDIR /home/desktop
ENTRYPOINT ["/usr/local/bin/start-desktop"]
