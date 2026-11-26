-- PUC Lua 5.1: math.log ignores a base; math.mod exists; math.max/min keep
-- the first of NaNs and zeros (the LuaJIT dialect's differ)
local z = 0
local nz = -z
print(math.log(8, 2) == math.log(8), math.log(100, 10) == math.log(100), type(math.mod), math.mod(7, 3))
print(math.max(0/0, 1, 2) ~= math.max(0/0, 1, 2), math.max(1, 0/0, 2), math.min(0/0, 2, 1) ~= 0, math.min(2, 1, 0/0))
print(1/math.max(nz, 0), 1/math.max(0, nz), 1/math.min(0, nz), 1/math.min(nz, 0))
