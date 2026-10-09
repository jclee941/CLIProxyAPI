FROM debian:bookworm-slim

ARG DEBIAN_FRONTEND=noninteractive
ARG CHROME_VERSION=

RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
        ca-certificates curl gnupg \
        xvfb x11vnc novnc websockify socat procps \
        fonts-liberation fonts-noto-cjk; \
    install -d -m 0755 /usr/share/keyrings; \
    curl -fsSL https://dl.google.com/linux/linux_signing_key.pub | gpg --dearmor -o /usr/share/keyrings/google-chrome.gpg; \
    echo "deb [arch=amd64 signed-by=/usr/share/keyrings/google-chrome.gpg] https://dl.google.com/linux/chrome/deb/ stable main" > /etc/apt/sources.list.d/google-chrome.list; \
    apt-get update; \
    apt-get install -y --no-install-recommends "google-chrome-stable${CHROME_VERSION:+=$CHROME_VERSION}"; \
    rm -rf /var/lib/apt/lists/*; \
    install -d -m 1777 /tmp/.X11-unix; \
    google-chrome-stable --version

COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod 0755 /usr/local/bin/entrypoint.sh

ENV DISPLAY=:99
VOLUME ["/config"]
EXPOSE 5900 6080 9223

HEALTHCHECK --interval=30s --timeout=5s --start-period=60s --retries=3 \
    CMD curl -fsS -o /dev/null http://127.0.0.1:9222/json/version && curl -fsS -o /dev/null http://127.0.0.1:6080/vnc.html

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
