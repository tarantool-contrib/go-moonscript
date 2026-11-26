class Person
  clothes: {}
  give_item: (name) =>
    table.insert @clothes, name

a = Person!
b = Person!

a\give_item "pants"
b\give_item "shirt"

-- will print both pants and shirt
print item for item in *a.clothes
