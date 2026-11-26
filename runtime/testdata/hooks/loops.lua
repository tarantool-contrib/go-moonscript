-- line events of loops: a backward jump to the same line fires again
local lines = {}
local function hook(event, line)
  lines[#lines + 1] = line
end

local function loops()
  local s = 0
  for i = 1, 3 do
    s = s + i
  end
  for i = 3, 1, -1 do s = s - i end
  for _, v in ipairs({10, 20}) do
    s = s + v
  end
  for k, v in pairs({a = 1}) do s = s + v end
  local j = 0
  while j < 3 do
    j = j + 1
    if j == 2 then
      s = s * 2
    else
      s = s + 1
    end
  end
  repeat
    j = j - 1
  until j == 0
  while true do
    j = j + 1
    if j > 2 then break end
  end
  for i = 1, 2 do
    for k = 1, 2 do
      s = s + i * k
    end
  end
  local t = {
    1,
    2,
    x = s,
  }
  return s, #t
end

debug.sethook(hook, "l")
local s, n = loops()
debug.sethook()

print(table.concat(lines, " "))
print(s, n)
