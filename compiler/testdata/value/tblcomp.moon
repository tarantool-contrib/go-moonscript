x = {k, v for k, v in pairs t}
y = {unpack t for t in pairs list}
z = {k, v * 2 for k, v in pairs t when v > 1}
w = {i, i for i = 1, 10}
