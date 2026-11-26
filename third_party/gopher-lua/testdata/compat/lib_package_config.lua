-- package.config is Lua 5.1's: five lines, no "\n" after the last one
print((string.gsub(package.config, "\n", "|")), #package.config)
local parts = {}
for line in string.gmatch(package.config, "[^\n]+") do parts[#parts + 1] = line end
print(#parts, parts[1], parts[2], parts[3], parts[4], parts[5])
