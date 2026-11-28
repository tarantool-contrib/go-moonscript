-- read("*n") reads a number as fscanf(f, "%lf") does on glibc
local name = os.tmpname()
for _, s in ipairs({"0x z", "0x.p1 z", ". z", "- z", "1e+ z", "1e+5z", "0x1p z", "0x1.8p1z", "infinityz", "nanz", "nan(123)z", "1.e3z", "-.e3z", "0X1Az", "00012z", "1e400z", "-1e400z", "1e-400z", "0x1p-1080z", "  \n\t 5z", "+-1z"}) do
  local f = io.open(name, "w") f:write(s) f:close()
  f = io.open(name)
  local n = f:read("*n")
  print(string.format("%q", s), n, string.format("%q", f:read("*a")))
  f:close()
end
os.remove(name)
