class Parent
class Child extends Parent

is_instance_of Child!, Child  -- true
is_instance_of Child!, Parent -- true (checks parent classes)
is_instance_of Parent!, Child -- false
