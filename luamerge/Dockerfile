# Dockerfile for luamerge (multi-platform)
#
# This only builds inside GoReleaser's own build context: GoReleaser stages
# each target's binary at $TARGETPLATFORM/luamerge next to this file before
# running `docker buildx build`, so a plain `docker build .` fails with "not
# found" on that COPY. Build images locally with
# `goreleaser release --snapshot --clean` (needs a docker buildx builder that
# supports multi-platform output). .dockerignore does not apply to that
# staged context.

FROM scratch

ARG TARGETPLATFORM
COPY $TARGETPLATFORM/luamerge /usr/local/bin/luamerge

ENTRYPOINT ["luamerge"]