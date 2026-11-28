class Parent
class Child extends Parent

is_subclass_of Child, Parent -- true
is_subclass_of Parent, Child -- false
is_subclass_of Child, Child  -- false
