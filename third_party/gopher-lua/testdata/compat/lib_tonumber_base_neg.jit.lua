-- tonumber(s, base) negates the value after a '-' (LuaJIT 2.1, Lua 5.2+ and
-- the Lua 5.1 test suite; PUC Lua 5.1 on 64-bit gives strtoul's 2^64 - n)
local cases = {"-1", "-10", "\n  -10  \n", "-0x10", "-ff", "-fFfa", "- 1", "--1", "-",
  "-ffffffffffffffff", "-1ffffffffffffffff", "-z"}
for i, s in ipairs(cases) do
  print(i, tonumber(s, 2), tonumber(s, 8), tonumber(s, 16), tonumber(s, 36))
end
print(tonumber(-1, 16), tonumber(-255, 36), tonumber('\n  -10  \n', 36) == -36)
