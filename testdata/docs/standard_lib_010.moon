class MyClass

x = MyClass!
assert type(x) == MyClass
assert type(MyClass) == "class"
assert type(MyClass.__base) == "table"
