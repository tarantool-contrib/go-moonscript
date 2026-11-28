-- yuin/gopher-lua#564: a call result indexed in the expressions of a generic for
local function q() return { { IND = "a|b" } } end
local out = {}
for field in ((q()[1].IND or "") .. "|"):gmatch("([^|]*)|") do out[#out + 1] = field end
print(#out)
local n = 0
for k, v in pairs(q()[1]) do n = n + 1 end
print(n)
for i, v in ipairs(q()[1].IND and {7,8} or {}) do print(i, v) end
for k in pairs(q()[1]) do print(k) end
local s = 0
for k in (q()[1].IND):gmatch("%a") do print(k) end
for k in next, q()[1] do print(k) end
for k in next, {q()} do print(k) end
local function three() return {10, 20}, "s", 0 end
for i, v in ipairs((three())) do print(i, v) end
for a, b in (function() return next, q()[1] end)() do print(a, b) end
for i = q()[1].IND:len(), #q()[1].IND + 1 do print(i) end
