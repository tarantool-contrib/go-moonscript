#!/usr/bin/lua
print("shebang ran", debug.getinfo(1, "l").currentline, debug.getinfo(1, "S").source)
return 7
