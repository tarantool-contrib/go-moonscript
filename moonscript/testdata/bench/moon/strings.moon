-- String building: interpolation, .. concatenation, table.concat buffers,
-- string.format, rep, sub, byte/char and gsub.
N = tonumber(arg and arg[1]) or 1000

words = {"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta"}

total = 0
last = ""
for i = 1, N
  w = words[i % #words + 1]
  s = "#{w}-#{i}: #{i * 2} (#{w\upper!})"
  buf = {}
  for j = 1, 8
    buf[#buf + 1] = "#{j}=#{words[j]}"
  line = table.concat buf, ","
  acc = ""
  for j = 1, 5
    acc = acc .. w\sub(1, j) .. ";"
  f = string.format "%5d|%-8s|%.2f", i, w, i / 7
  r = (w\rep 3)\gsub "a", "A"
  c = string.char w\byte(1), w\byte(-1)
  total += #s + #line + #acc + #f + #r + #c
  last = s if i % 1000 == 0

print total, last
