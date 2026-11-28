require "moon.all"
print "globals", type(p), type(run_with_scope), type(fold), type(is_class), type(defaultbl)
p {"from", "all"}
print fold {1, 2, 3}, (a, b) -> a .. b
