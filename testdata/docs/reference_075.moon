class Shelf
  @__inherited: (child) =>
    print @__name, "was inherited by", child.__name

-- will print: Shelf was inherited by Cupboard
class Cupboard extends Shelf
