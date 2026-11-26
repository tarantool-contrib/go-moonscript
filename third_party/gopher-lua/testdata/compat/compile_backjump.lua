-- yuin/gopher-lua#540: a backward jump to an already patched jump
local function t(s)
  local f = assert(loadstring(s))
  print(pcall(f))
end
t[[for _, v in ipairs({1}) do if false then local unused end end return 42]]
t[[local n = 0 for _, v in ipairs({1, 2, 3}) do if false then local unused end n = n + v end return n]]
t[[local a, b = 17, 23 for _, v in ipairs({1}) do if false then local unused end end return a, b]]
t[[local n = 0 for _, v in ipairs({1, 2, 3}) do if false then break end n = n + v end return n]]
t[[for _, v in ipairs({1}) do if false then for k in pairs({}) do end end end return 42]]
t[[local n = 0 for v = 1, 3 do if false then local unused end n = n + v end return n]]
t[[local n = 0 while n < 3 do if false then local unused end n = n + 1 end return n]]
t[[local n = 0 repeat if false then local unused end n = n + 1 until n >= 3 return n]]
