-- luabench wrapper, the same for every engine:
--
--   <lua> wrap.lua <script> [args...]
--
-- runs <script> the way lua.c runs a script (the global arg with arg[0] the
-- script, arg[1..] and ... its arguments; the script's directory first on
-- package.path) and writes the CPU seconds its run took to stderr as
-- "LUABENCH_CPU <seconds>". The clock is os.clock (clock(3): process CPU
-- time) unless the host installed LUABENCH_CLOCK (our VM: getrusage, since
-- gopher-lua's os.clock is wall time). Loading the wrapper, the
-- interpreter's startup and compiling <script> are outside the measurement.
local clock = rawget(_G, "LUABENCH_CLOCK") or os.clock
rawset(_G, "LUABENCH_CLOCK", nil)
local script = assert(arg and arg[1], "usage: wrap.lua <script> [args...]")
local args = {}
for i = 2, #arg do
  args[i - 1] = arg[i]
end
arg = { [0] = script }
for i = 1, #args do
  arg[i] = args[i]
end
local dir = script:match("^(.*)/[^/]*$") or "."
package.path = dir .. "/?.lua;" .. package.path
local f = assert(loadfile(script))
local t0 = clock()
f(unpack(args))
io.stdout:flush()
local t1 = clock()
io.stderr:write(string.format("LUABENCH_CPU %.6f\n", t1 - t0))
