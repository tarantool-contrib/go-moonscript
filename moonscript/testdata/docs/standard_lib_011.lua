local obj = SomeClass()
local bound_obj = bind_methods(obj)
obj:hello()
return bound_obj.hello()