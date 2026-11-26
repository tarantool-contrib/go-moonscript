# The reference implementation the port is checked against: upstream
# MoonScript v0.8.0 (its moonc, moon and Lua modules, with the C parser)
# on PUC Lua 5.1. Build it with scripts/moonref/build.sh.
FROM debian:bookworm-slim

RUN apt-get update \
	&& apt-get install -y --no-install-recommends \
		lua5.1 liblua5.1-0-dev luarocks gcc libc6-dev make git ca-certificates \
	&& rm -rf /var/lib/apt/lists/*

ADD --checksum=sha256:0458ba86359c9089f823f81e4198ea3f7d57c2f2773fb8a1364940ebdd802ac5 \
	https://github.com/leafo/moonscript/archive/refs/tags/v0.8.0.tar.gz /tmp/moonscript.tar.gz

RUN mkdir /moonscript \
	&& tar -xzf /tmp/moonscript.tar.gz -C /moonscript --strip-components=1 \
	&& rm /tmp/moonscript.tar.gz

WORKDIR /moonscript
RUN luarocks install lpeg 1.1.0-2 \
	&& luarocks install argparse 0.7.2-1 \
	&& luarocks install luafilesystem 1.9.0-1 \
	&& luarocks make moonscript-dev-1.rockspec

WORKDIR /work
