# Dockerfile for luamerge (multi‑platform)
# GoReleaser will place the built binary in $TARGETPLATFORM/ directory

FROM scratch

ARG TARGETPLATFORM
COPY $TARGETPLATFORM/luamerge /usr/local/bin/luamerge

ENTRYPOINT ["luamerge"]