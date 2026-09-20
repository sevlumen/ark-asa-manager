ARG PROTON_VERSION=GE-Proton10-34
ARG PROTON_SHA256=51c580b66a833c73998fe00f0717eeac57197654040a2f2ed5189e3ee68d773d
ARG STEAMCMD_SHA256=cebf0046bfd08cf45da6bc094ae47aa39ebf4155e5ede41373b579b8f1071e7c
ARG STEAMCMD_URL=https://steamcdn-a.akamaihd.net/client/installer/steamcmd_linux.tar.gz
ARG PROTON_URL=
ARG VC_REDIST_SHA256=CC0FF0EB1DC3F5188AE6300FAEF32BF5BEEBA4BDD6E8E445A9184072096B713B
ARG VC_REDIST_URL=https://download.visualstudio.microsoft.com/download/pr/bd1c8d9d-ba95-4eee-bc6e-df1fcc876373/CC0FF0EB1DC3F5188AE6300FAEF32BF5BEEBA4BDD6E8E445A9184072096B713B/VC_redist.x64.exe

FROM ubuntu:24.04 AS runtime-bundle

ARG DEBIAN_FRONTEND=noninteractive
ARG PROTON_VERSION
ARG PROTON_SHA256
ARG STEAMCMD_SHA256
ARG STEAMCMD_URL
ARG PROTON_URL
ARG VC_REDIST_SHA256
ARG VC_REDIST_URL

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl lib32gcc-s1 tar xz-utils \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --create-home --uid 10001 --shell /usr/sbin/nologin ark \
    && mkdir -p /opt/ark/game /opt/ark/data/{save,config,log,backups,cluster} \
    && chown -R ark:ark /opt/ark

COPY --chown=ark:ark deploy/docker/ark-runtime/download-runtime.sh /usr/local/bin/download-runtime.sh
RUN chmod 0755 /usr/local/bin/download-runtime.sh

USER ark
WORKDIR /opt/ark

RUN proton_url="${PROTON_URL:-https://github.com/GloriousEggroll/proton-ge-custom/releases/download/${PROTON_VERSION}/${PROTON_VERSION}.tar.gz}" \
    && /usr/local/bin/download-runtime.sh "$STEAMCMD_URL" /tmp/steamcmd_linux.tar.gz "$STEAMCMD_SHA256" \
    && tar -xzf /tmp/steamcmd_linux.tar.gz -C /opt/ark \
    && rm -f /tmp/steamcmd_linux.tar.gz \
    && /usr/local/bin/download-runtime.sh "$proton_url" /tmp/${PROTON_VERSION}.tar.gz "$PROTON_SHA256" \
    && mkdir -p /opt/ark/proton \
    && tar -xzf /tmp/${PROTON_VERSION}.tar.gz --strip-components=1 -C /opt/ark/proton \
    && rm -f /tmp/${PROTON_VERSION}.tar.gz \
    && /usr/local/bin/download-runtime.sh "$VC_REDIST_URL" /opt/ark/vc_redist.x64.exe "$VC_REDIST_SHA256"

FROM ubuntu:24.04

ARG DEBIAN_FRONTEND=noninteractive

RUN dpkg --add-architecture i386 \
    && apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates dbus lib32gcc-s1 libasound2t64 libasound2t64:i386 libcups2 libcups2:i386 libfontconfig1 libfontconfig1:i386 libfreetype6 libfreetype6:i386 libgl1 libgl1-mesa-dri libglib2.0-0 libglib2.0-0:i386 libnss3 libnss3:i386 libopenal1 libopenal1:i386 libvulkan1 libvulkan1:i386 netcat-openbsd procps python3 tar x11-xserver-utils xauth xvfb xz-utils \
    && rm -rf /var/lib/apt/lists/* \
    && rm -f /etc/machine-id /var/lib/dbus/machine-id \
    && dbus-uuidgen --ensure=/etc/machine-id \
    && dbus-uuidgen --ensure \
    && useradd --create-home --uid 10001 --shell /usr/sbin/nologin ark \
    && mkdir -p /opt/ark/game /opt/ark/data/{save,config,log,backups,cluster} \
    && chown -R ark:ark /opt/ark

COPY --from=runtime-bundle --chown=ark:ark /opt/ark /opt/ark
COPY --chown=ark:ark deploy/docker/ark-runtime/entrypoint.sh /usr/local/bin/entrypoint.sh
COPY --chown=ark:ark deploy/docker/ark-runtime/secrets.sh /usr/local/bin/secrets.sh
COPY --chown=ark:ark deploy/docker/ark-runtime/healthcheck.sh /usr/local/bin/healthcheck.sh
RUN chmod 0755 /usr/local/bin/entrypoint.sh /usr/local/bin/healthcheck.sh

USER ark
WORKDIR /opt/ark

EXPOSE 7777/udp 27015/udp 32330/tcp
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
