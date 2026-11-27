-- A value moved out of a table's array part when it grows, then removed,
-- is collected: tables with a small array part get it in the same
-- allocation (go-moonscript), which must not keep the old values alive.
for size = 1, 5 do
  local w = setmetatable({}, {__mode = "v"})
  local t = {}
  for i = 1, size do t[i] = {} end
  local ctor = ({{}, {{}, {}}, {{}, {}, {}}, {{}, {}, {}, {}}, {{}, {}, {}, {}, {}}})[size]
  for i = 1, size do w[i] = t[i]; w[size + i] = ctor[i] end
  for i = size + 1, 4 * size + 8 do t[i] = i; ctor[i] = i end -- grow
  for i = 1, size do t[i] = nil; ctor[i] = nil end
  collectgarbage(); collectgarbage()
  local left = 0
  for _ in pairs(w) do left = left + 1 end
  print(size, "weak values left", left)
  keep = {t, ctor}
end
