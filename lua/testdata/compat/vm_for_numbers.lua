-- Lua 5.1's numeric for: the initial value, the limit and the step are
-- checked, in that order, before the loop starts, and strings convertible
-- to numbers are converted ("'for' initial value must be a number"; was
-- "for statement init must be a number", strings rejected, the limit
-- checked by the first iteration only).
local function p(...) print(pcall(...)) end
p(function() for i = nil, 2 do end end)
p(function() for i = 1, nil do end end)
p(function() for i = 1, 2, {} do end end)
p(function() for i = 1, {}, nil do end end)
p(function() for i = 1, 0, nil do end end)
p(function() for i = {}, nil, nil do end end)
p(function() for i = "x", 2 do end end)
p(function() for i = 1, "2x" do end end)
p(function() for i = "1", "3" do print(i, type(i)) end end)
p(function() for i = " 0x10 ", "17", "1" do print(i, type(i)) end end)
p(function() for i = 1, 2, "0.5" do print(i) end end)
p(function() for i = 3, 1, "-1" do print(i) end end)
p(function() for i = "1e1", 10 do print(i) end end)
p(function() local function first() return end for i = first(), 2 do end end)
p(function() local function limit() return end for i = 1, limit() do end end)
p(function() local function step() return end for i = 1, 2, step() do end end)
